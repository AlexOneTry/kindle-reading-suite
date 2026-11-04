#!/usr/bin/env python3
"""Local tools for reading on Kindle and KOReader."""
import argparse
import hashlib
import gzip
import json
import io
import os
from pathlib import Path
import sqlite3
import string
import sys
import tarfile
from tempfile import NamedTemporaryFile, TemporaryDirectory
from xml.etree import ElementTree as ET

from articles import fetch, feed_entries, save_article, web_url
from notes import write_reports
from words import export_words

VERSION = "1.1.0"
ROOT = Path(__file__).resolve().parent
DEFAULT = Path.home() / "Documents" / "KOReader"
DICTIONARY = "FreeDict English-Russian"
ARCHIVE = ROOT / "dictionary" / "freedict-eng-rus.stardict.tar.xz"
CHECKSUM = ROOT / "dictionary" / "freedict-eng-rus.stardict.tar.xz.sha512"


def volume_roots(system=None):
    if (system or os.name) == "nt":
        return [letter + ":\\" for letter in string.ascii_uppercase[3:]]
    return list(Path("/Volumes").glob("*"))


def detect_kindles(candidates=None):
    found = []
    for candidate in volume_roots() if candidates is None else candidates:
        path = Path(candidate)
        try:
            if (path / "koreader" / "reader.lua").is_file():
                found.append(path)
        except OSError:
            continue
    return found


def kindle_at(path):
    if not (path / "koreader" / "reader.lua").is_file():
        raise ValueError("в указанной папке нет установленного KOReader")
    return path


def write_once(path, data):
    if path.exists():
        if path.read_bytes() != data:
            raise ValueError(f"другой файл уже существует: {path.name}")
        return
    with NamedTemporaryFile(dir=path.parent, delete=False) as f:
        f.write(data)
        temp = Path(f.name)
    try:
        temp.replace(path)
    finally:
        temp.unlink(missing_ok=True)


def dictionary_files():
    data = ARCHIVE.read_bytes()
    checksum = CHECKSUM.read_text(encoding="ascii").split()[0]
    if hashlib.sha512(data).hexdigest() != checksum:
        raise ValueError("контрольная сумма словаря FreeDict не совпала")
    files = {}
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:xz") as archive:
        for entry in archive:
            name = Path(entry.name).name
            if not entry.isfile() or not name or entry.size > 32 * (1 << 20):
                continue
            if name.endswith((".ifo", ".idx", ".idx.gz", ".dict", ".dict.dz", ".syn")) or name.lower() in {"copying", "license", "readme"}:
                if name in files:
                    raise ValueError("в словаре повторяется имя файла")
                with archive.extractfile(entry) as source:
                    files[name] = source.read()
    for name in list(files):
        if name.endswith(".idx.gz"):
            files[name.removesuffix(".gz")] = gzip.decompress(files.pop(name))
    indexes = [name.removesuffix(".idx") for name in files if name.endswith(".idx")]
    if len(indexes) != 1 or indexes[0]+".ifo" not in files or not any(indexes[0]+s in files for s in (".dict", ".dict.dz")):
        raise ValueError("архив не содержит полный словарь StarDict")
    return files


def install_dictionary(kindle):
    kindle_at(kindle)
    files = dictionary_files()
    target = kindle / "koreader" / "data" / "dict" / DICTIONARY
    target.mkdir(parents=True, exist_ok=True)
    for name, content in files.items():
        if (target / name).exists() and (target / name).read_bytes() != content:
            raise ValueError(f"в папке уже есть другая версия: {name}")
    for name, content in files.items():
        write_once(target / name, content)
    print(f"Словарь установлен: {target}")


def install_translation(kindle):
    kindle_at(kindle)
    target = kindle / "koreader" / "patches"
    target.mkdir(parents=True, exist_ok=True)
    source = ROOT / "2-reading-kit-russian.lua"
    write_once(target / source.name, source.read_bytes())
    print("При следующем запуске KOReader выберет русский язык для перевода фраз.")


def save_feed(url, target, limit):
    data, _, final = fetch(url)
    entries = list(feed_entries(data, final))
    if not entries:
        raise ValueError("в ленте нет записей со ссылками")
    done, seen = 0, set()
    for link, title, body in entries:
        if link in seen:
            continue
        seen.add(link)
        if len(seen) > limit:
            break
        try:
            print(save_article(link, target, title, body))
            done += 1
        except (OSError, ValueError) as exc:
            print(f"Пропущена запись «{title or 'без названия'}»: {exc}", file=sys.stderr)
    if done == 0:
        raise ValueError("ни одну запись не удалось сохранить")


def words_from_kindle(kindle, output):
    kindle_at(kindle)
    dict_root = kindle / "koreader" / "data" / "dict" / DICTIONARY
    if dict_root.is_dir():
        return export_words(kindle / "koreader/settings/vocabulary_builder.sqlite3", dict_root, output)
    with TemporaryDirectory(prefix="koreader-freedict-") as folder:
        lookup = Path(folder)
        for name, content in dictionary_files().items():
            (lookup / name).write_bytes(content)
        return export_words(kindle / "koreader/settings/vocabulary_builder.sqlite3", lookup, output)


def pick_kindle():
    kindles = detect_kindles()
    if len(kindles) == 1:
        print(f"Kindle: {kindles[0]}")
        return kindles[0]
    value = input("Путь к USB-диску Kindle: ").strip().strip('"')
    if not value:
        raise ValueError("Kindle не выбран")
    return kindle_at(Path(value).expanduser())


def choose_feed():
    path = DEFAULT / "RSS.json"
    feeds = []
    if path.is_file():
        stored = json.loads(path.read_text(encoding="utf-8"))
        if isinstance(stored, list):
            feeds = [item for item in stored if isinstance(item, str)][:30]
    for i, url in enumerate(feeds, 1):
        print(f"{i}. {url}")
    choice = input("Номер сохранённой ленты или новая ссылка RSS/Atom: ").strip()
    if choice.isdigit() and 1 <= int(choice) <= len(feeds):
        return feeds[int(choice)-1]
    url = web_url(choice)
    if url not in feeds and input("Запомнить эту ленту? [д/Н] ").strip().lower() in {"д", "да", "y", "yes"}:
        DEFAULT.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps([url] + feeds[:29], ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return url


def menu():
    print("KOReader · Статьи, конспекты и слова")
    print("1. Сохранить статью по ссылке")
    print("2. Скачать свежие статьи из RSS или Atom")
    print("3. Собрать конспекты из заметок")
    print("4. Выгрузить слова для повторения")
    print("5. Установить англо-русский словарь и настроить перевод")
    action = input("Выбери номер: ").strip()
    if action in {"1", "2"}:
        url = input("Ссылка: ").strip() if action == "1" else choose_feed()
        dest = DEFAULT / "Статьи"
        readers = detect_kindles()
        if len(readers) == 1 and input("Сохранить прямо на Kindle? [д/Н] ").strip().lower() in {"д", "да", "y", "yes"}:
            dest = readers[0] / "Статьи"
        if action == "1":
            print(save_article(url, dest))
        else:
            save_feed(url, dest, 5)
    elif action == "3":
        target, count = write_reports(pick_kindle(), DEFAULT / "Заметки")
        print(f"Книг с заметками: {count}. Выгрузка: {target}")
    elif action == "4":
        target, count = words_from_kindle(pick_kindle(), DEFAULT / "Слова")
        print(f"Слов: {count}. Выгрузка: {target}")
    elif action == "5":
        kindle = pick_kindle()
        install_dictionary(kindle)
        install_translation(kindle)
    else:
        raise ValueError("нет такого пункта")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", action="version", version=VERSION)
    commands = parser.add_subparsers(dest="command")
    for name in ("article", "feed"):
        p = commands.add_parser(name)
        p.add_argument("url")
        p.add_argument("--output", type=Path, default=DEFAULT / "Статьи")
        if name == "feed":
            p.add_argument("--limit", type=int, choices=range(1, 21), default=5, metavar="1–20")
    p = commands.add_parser("notes")
    p.add_argument("source", type=Path)
    p.add_argument("--output", type=Path, default=DEFAULT / "Заметки")
    p = commands.add_parser("words")
    p.add_argument("kindle", type=Path)
    p.add_argument("--output", type=Path, default=DEFAULT / "Слова")
    p = commands.add_parser("install")
    p.add_argument("kindle", type=Path)
    p.add_argument("--translation", action="store_true")
    args = parser.parse_args()
    try:
        if args.command == "article":
            print(save_article(args.url, args.output))
        elif args.command == "feed":
            save_feed(args.url, args.output, args.limit)
        elif args.command == "notes":
            if not args.source.is_dir():
                raise ValueError("папка с книгами не найдена")
            target, count = write_reports(args.source, args.output)
            print(f"Книг с заметками: {count}. Выгрузка: {target}")
        elif args.command == "words":
            target, count = words_from_kindle(args.kindle, args.output)
            print(f"Слов: {count}. Выгрузка: {target}")
        elif args.command == "install":
            install_dictionary(args.kindle)
            if args.translation:
                install_translation(args.kindle)
        else:
            menu()
    except (ValueError, OSError, EOFError, ET.ParseError, tarfile.TarError, sqlite3.Error) as exc:
        print(f"Не получилось: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
