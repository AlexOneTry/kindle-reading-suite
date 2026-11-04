#!/usr/bin/env python3
"""Установка комплекта на Kindle с проверкой совместимости."""
import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys
from tempfile import NamedTemporaryFile

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT / "Reading-Tools"))
import reading
from connect_yandex import pair

VERSION = "1.0.0"
REPORT_PATH = Path("extensions/reading-kit-check/report.json")


def verify_payload():
    data = json.loads((ROOT / "MANIFEST.json").read_text(encoding="utf-8"))
    for name, digest in data.items():
        path = ROOT / name
        if Path(name).is_absolute() or ".." in Path(name).parts or path.is_symlink():
            raise ValueError("неверный путь в составе комплекта")
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError(f"файл повреждён или изменён: {name}. Распакуй архив заново")


def find_devices():
    # Marker files identify a KOReader installation, not the drive's display name.
    return reading.detect_kindles()


def select_device():
    devices = find_devices()
    if len(devices) == 1:
        return devices[0]
    if devices:
        for index, path in enumerate(devices, 1):
            print(f"{index}. {path}")
        choice = input("Номер Kindle: ").strip()
        if choice.isdigit() and 1 <= int(choice) <= len(devices):
            return devices[int(choice)-1]
        raise ValueError("устройство не выбрано")
    print("Kindle как USB-диск не найден. Выйди из KOReader и подключи USB-кабель с передачей данных.")
    print("Если устройство использует MTP, смотри Инструкции/MTP.md. Автокопирование по MTP не поддерживается.")
    value = input("Путь к диску Kindle (Enter — отмена): ").strip().strip('"').strip("'")
    if not value:
        raise ValueError("Kindle не выбран")
    return reading.kindle_at(Path(value).expanduser())


def read_report(path):
    if not path.is_file() or path.stat().st_size > 16384:
        raise ValueError("отчёт проверки отсутствует или повреждён")
    report = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(report, dict) or report.get("schema") != 1 or report.get("probe_version") != "1.0.0":
        raise ValueError("неизвестный формат отчёта Kindle")
    return report


def incompatibilities(report):
    if report.get("probe_error"):
        return ["Проверочная программа ARMv7 не запустилась. Для этой платформы нужна отдельная сборка."]
    reasons = []
    if report.get("runtime") != "arm":
        reasons.append("Нужна проверенная 32-битная платформа ARM. Для другой архитектуры нужна отдельная сборка.")
    if report.get("root") is not True:
        reasons.append("Проверка не получила права root через jailbreak/KUAL.")
    if report.get("touch") is not True:
        reasons.append("Не найден совместимый сенсорный экран. Управление кнопками в Yandex Books 0.6.0 не реализовано.")
    if report.get("fbink_ok") is not True:
        reasons.append("FBInk не запустился или не распознал экран.")
    w, h = report.get("width"), report.get("height")
    if type(w) is not int or type(h) is not int or not (600 <= w < h <= 4096):
        reasons.append("Нужна книжная ориентация экрана шириной от 600 и высотой до 4096 точек.")
    return reasons


def describe(report):
    print("Модель: " + str(report.get("model") or "название не передано устройством"))
    print("Прошивка: " + str(report.get("firmware") or "не определена"))
    print(f"Платформа: {report.get('machine', 'не определена')}; экран: {report.get('width', '?')} × {report.get('height', '?')}")
    reasons = incompatibilities(report)
    if reasons:
        for reason in reasons:
            print("• " + reason)
    else:
        print("Проверка среды пройдена. Работу касаний и чтения нужно проверить после запуска приложения.")
    return not reasons


def replace_file(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    with NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        temp = Path(handle.name)
        handle.write(data)
    try:
        temp.replace(path)
    finally:
        temp.unlink(missing_ok=True)


def apply_files(target, files, backup_root=None):
    """Back up changed files and restore them if any write fails."""
    target = target.resolve()
    planned = []
    for relative, data in files.items():
        rel = Path(relative)
        path = target / rel
        if rel.is_absolute() or ".." in rel.parts or target not in path.resolve().parents:
            raise ValueError("небезопасный путь установки")
        if any(parent.is_symlink() for parent in [path, *path.parents] if parent != target.parent):
            raise ValueError("в пути установки найдена символическая ссылка")
        old = path.read_bytes() if path.is_file() else None
        if old != data:
            planned.append((rel, data, old))
    if not planned:
        print("Все выбранные файлы уже установлены.")
        return None
    backup_root = backup_root or Path.home() / "Documents" / "KOReader" / "Резервные копии"
    backup = backup_root / datetime.now().astimezone().strftime("Kindle-%Y%m%d-%H%M%S-%f")
    backup.mkdir(parents=True)
    for rel, _, old in planned:
        if old is not None:
            stored = backup / rel
            stored.parent.mkdir(parents=True, exist_ok=True)
            stored.write_bytes(old)
    (backup / "installation.json").write_text(json.dumps({
        "created_at": datetime.now().astimezone().isoformat(),
        "files": [{"path": rel.as_posix(), "replaced": old is not None} for rel, _, old in planned],
    }, ensure_ascii=False, indent=2), encoding="utf-8")
    written = []
    try:
        for rel, data, old in planned:
            written.append((rel, old))
            replace_file(target / rel, data)
    except OSError:
        for rel, old in reversed(written):
            if old is None:
                (target / rel).unlink(missing_ok=True)
            else:
                replace_file(target / rel, old)
        raise
    print(f"Готово. Записано файлов: {len(planned)}. Резервная копия: {backup}")
    return backup


def directory_files(source, prefix=""):
    return {(Path(prefix) / file.relative_to(source)).as_posix(): file.read_bytes()
            for file in source.rglob("*") if file.is_file()}


def prepare_check(target):
    # A valid USB root is required; installing a diagnostic is not a jailbreak.
    reading.kindle_at(target)
    files = directory_files(ROOT / "Compatibility-Check", "extensions/reading-kit-check")
    apply_files(target, files)
    print("Безопасно извлеки Kindle. Открой KUAL → Reading Kit: check compatibility.")
    print("Снова подключи Kindle и запусти установщик. Он прочитает результат и продолжит установку.")


def tool_files():
    files = {f"koreader/data/dict/{reading.DICTIONARY}/{name}": data
             for name, data in reading.dictionary_files().items()}
    name = "2-reading-kit-russian.lua"
    files["koreader/patches/" + name] = (ROOT / "Reading-Tools" / name).read_bytes()
    return files


def install(target, tools_only=False):
    reading.kindle_at(target)
    if tools_only:
        return apply_files(target, tool_files())
    path = target / REPORT_PATH
    if not path.is_file():
        prepare_check(target)
        return None
    report = read_report(path)
    if not describe(report):
        raise ValueError("Yandex Books не установлен. Доступна отдельная установка словаря и настройки перевода")
    files = directory_files(ROOT / "Yandex-Books" / "extensions", "extensions")
    files.update(directory_files(ROOT / "Yandex-Books" / "koreader", "koreader"))
    # The dictionary is a separate, optional action, as existing dictionaries can be large.
    result = apply_files(target, files)
    print("Yandex Books 0.6.0 установлен. Для первого входа открой команду подключения в папке Yandex-Books.")
    print("Извлеки Kindle и открой KUAL → Yandex Books. Книги и заметки остаются на своих местах.")
    return result


def stage(report_file, output):
    """Prepare MTP files on the computer; no implicit device writes."""
    if report_file is None:
        files = directory_files(ROOT / "Compatibility-Check", "extensions/reading-kit-check")
    else:
        report = read_report(report_file)
        if not describe(report):
            raise ValueError("для этого устройства нет подходящей сборки Yandex Books")
        files = directory_files(ROOT / "Yandex-Books" / "extensions", "extensions")
        files.update(directory_files(ROOT / "Yandex-Books" / "koreader", "koreader"))
    output.mkdir(parents=True, exist_ok=True)
    apply_files(output, files)
    print("Это папка для ручного переноса через MTP. Скопируй её содержимое в память Kindle.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", action="version", version=VERSION)
    subs = parser.add_subparsers(dest="command")
    for name in ("install", "inspect", "check", "connect"):
        p = subs.add_parser(name)
        p.add_argument("kindle", type=Path)
        if name == "install":
            p.add_argument("--tools-only", action="store_true")
    p = subs.add_parser("stage")
    p.add_argument("--report", type=Path)
    p.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        verify_payload()
        if args.command == "install":
            install(args.kindle, args.tools_only)
        elif args.command == "check":
            prepare_check(args.kindle)
        elif args.command == "connect":
            pair(args.kindle)
        elif args.command == "inspect":
            reading.kindle_at(args.kindle)
            return 0 if describe(read_report(args.kindle / REPORT_PATH)) else 1
        elif args.command == "stage":
            stage(args.report, args.output)
        else:
            print(f"Kindle Reading Suite {VERSION}")
            print("1. Проверить Kindle и установить Yandex Books")
            print("2. Установить словарь и русский язык перевода KOReader")
            print("3. Открыть меню статей, заметок и слов на компьютере")
            print("4. Подключить аккаунт Яндекс к Kindle")
            print("0. Выйти")
            choice = input("Выбери номер: ").strip()
            if choice == "0":
                return 0
            if choice == "3":
                return reading.main()
            if choice == "4":
                pair(select_device())
                return 0
            if choice not in {"1", "2"}:
                raise ValueError("нет такого пункта")
            install(select_device(), tools_only=choice == "2")
    except (OSError, ValueError, EOFError) as exc:
        print(f"Не получилось: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
