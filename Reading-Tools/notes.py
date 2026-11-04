#!/usr/bin/env python3
"""Export KOReader EPUB sidecars as data. Lua is never executed."""
import argparse
from datetime import datetime
import json
from pathlib import Path
import re
import string
import sys
import zipfile
from xml.etree import ElementTree as XML

NUMBER = re.compile(r"-?(?:0[xX][0-9A-Fa-f]+|\d+(?:\.\d*)?(?:[eE][+-]?\d+)?)")
WORD = re.compile(r"[A-Za-z_][A-Za-z_0-9]*")
LONG = re.compile(r"\[(=*)\[")
SPACE = re.compile(r"\s+")


class LuaData:
    def __init__(self, source):
        self.s = source
        self.i = 0
        self.token = self.read()

    def read(self):
        s = self.s
        while self.i < len(s):
            whitespace = SPACE.match(s, self.i)
            if whitespace:
                self.i = whitespace.end()
            elif s.startswith("--", self.i):
                self.i += 2
                if LONG.match(s, self.i):
                    self.read_long()
                else:
                    end = s.find("\n", self.i)
                    self.i = len(s) if end < 0 else end + 1
            else:
                break
        if self.i == len(s):
            return "eof", None
        c = s[self.i]
        if LONG.match(s, self.i):
            return "value", self.read_long()
        if c in "'\"":
            return "value", self.read_string(c)
        match = NUMBER.match(s, self.i)
        if match:
            self.i = match.end()
            value = match[0]
            try:
                return "value", int(value, 0)
            except ValueError:
                return "value", float(value)
        match = WORD.match(s, self.i)
        if match:
            self.i = match.end()
            return "word", match[0]
        self.i += 1
        return c, c

    def read_long(self):
        opening = LONG.match(self.s, self.i)
        tail = "]" + opening[1] + "]"
        end = self.s.find(tail, opening.end())
        if end < 0:
            raise ValueError("незакрытая строка Lua")
        self.i = end + len(tail)
        return self.s[opening.end():end].removeprefix("\n")

    def read_string(self, quote):
        self.i += 1
        out = bytearray()
        escapes = {"a": "\a", "b": "\b", "f": "\f", "n": "\n", "r": "\r",
                   "t": "\t", "v": "\v", "\n": "\n", "\r": "\n"}
        while self.i < len(self.s):
            c = self.s[self.i]
            self.i += 1
            if c == quote:
                return out.decode("utf-8", "replace")
            if c != "\\":
                out.extend(c.encode("utf-8"))
                continue
            if self.i == len(self.s):
                break
            c = self.s[self.i]
            self.i += 1
            if c.isascii() and c.isdigit():
                m = re.match(r"\d{1,3}", self.s[self.i - 1:])
                out.append(int(m[0]))
                self.i += len(m[0]) - 1
            elif c == "x" and self.i + 2 <= len(self.s) and all(
                x in string.hexdigits for x in self.s[self.i:self.i + 2]
            ):
                out.append(int(self.s[self.i:self.i + 2], 16))
                self.i += 2
            elif c == "z":
                m = SPACE.match(self.s, self.i)
                if m:
                    self.i = m.end()
            else:
                out.extend(escapes.get(c, c).encode("utf-8"))
        raise ValueError("незакрытая строка Lua")

    def pop(self, kind):
        actual, value = self.token
        if actual != kind:
            raise ValueError(f"ожидалось {kind} в таблице Lua")
        self.token = self.read()
        return value

    def value(self, depth=0):
        if depth > 80:
            raise ValueError("слишком глубокая таблица Lua")
        kind, value = self.token
        if kind == "value":
            return self.pop("value")
        if kind == "word" and value in ("true", "false", "nil"):
            self.pop("word")
            return {"true": True, "false": False, "nil": None}[value]
        self.pop("{")
        table, index = {}, 1
        while self.token[0] != "}":
            if self.token[0] == "[":
                self.pop("[")
                key = self.value(depth + 1)
                self.pop("]")
                self.pop("=")
                table[key] = self.value(depth + 1)
            elif self.token[0] == "word" and self.token[1] not in ("true", "false", "nil"):
                key = self.pop("word")
                self.pop("=")
                table[key] = self.value(depth + 1)
            else:
                table[index] = self.value(depth + 1)
                index += 1
            if self.token[0] in (",", ";"):
                self.pop(self.token[0])
            elif self.token[0] != "}":
                raise ValueError("неверный разделитель Lua")
        self.pop("}")
        return table

    def parse(self):
        if self.pop("word") != "return":
            raise ValueError("ожидалась таблица KOReader")
        result = self.value()
        if not isinstance(result, dict) or self.token[0] != "eof":
            raise ValueError("неверный формат файла KOReader")
        return result


def epub_metadata(file):
    with zipfile.ZipFile(file) as epub:
        container = XML.fromstring(epub.read("META-INF/container.xml"))
        rootfile = next(n for n in container.iter() if n.tag.split("}")[-1] == "rootfile")
        package = XML.fromstring(epub.read(rootfile.attrib["full-path"]))
        metadata = next(n for n in package if n.tag.split("}")[-1] == "metadata")
        return [
            {"field": n.tag.split("}")[-1], "value": "".join(n.itertext()).strip(),
             "attributes": {k.split("}")[-1]: v for k, v in n.attrib.items()}}
            for n in metadata
        ]


def collect(root):
    for sidecar in sorted(root.rglob("metadata.epub.lua")):
        try:
            if sidecar.stat().st_size > 16 * (1 << 20):
                raise ValueError("слишком большой файл заметок")
            settings = LuaData(sidecar.read_text(encoding="utf-8-sig")).parse()
            annotations = [a for a in settings.get("annotations", {}).values()
                           if isinstance(a, dict) and (a.get("text") or a.get("note"))]
            if not annotations:
                continue
            book = sidecar.parent.with_suffix(".epub")
            properties = settings.get("doc_props") or {}
            try:
                fields = epub_metadata(book)
            except (OSError, ValueError, zipfile.BadZipFile, XML.ParseError, StopIteration, KeyError):
                fields = []
            title = next((m["value"] for m in fields if m["field"] == "title"), None)
            author = ", ".join(m["value"] for m in fields if m["field"] == "creator")
            notes = [{
                "text": a.get("text"), "note": a.get("note"),
                "chapter": a.get("chapter"), "page": a.get("pageno", a.get("page")),
                "created_at_device": a.get("datetime"), "updated_at_device": a.get("datetime_updated"),
                "start": a.get("pos0"), "end": a.get("pos1"),
            } for a in annotations]
            yield {"title": title or properties.get("title") or book.stem,
                   "author": author or properties.get("authors"), "epub_metadata": fields,
                   "koreader_metadata": properties, "file": book.relative_to(root).as_posix(), "notes": notes}
        except (OSError, ValueError, TypeError, AttributeError, RecursionError) as exc:
            print(f"Пропущен {sidecar.parent.name}: {exc}", file=sys.stderr)


def markdown(report):
    lines = ["# Заметки KOReader", "", f"Выгружено {report['exported_at']}", ""]
    for book in report["books"]:
        lines.extend([f"## {book['title']}", "", str(book["author"] or "Автор не указан"), ""])
        for field in book["epub_metadata"]:
            if field["field"] not in ("title", "creator") and field["value"]:
                lines.append(f"- {field['field']}: {field['value']}")
        for note in book["notes"]:
            lines.extend(["", f"### {note['chapter'] or 'Заметка'}", ""])
            if note["created_at_device"]:
                lines.extend([f"Создано на Kindle: {note['created_at_device']}", ""])
            if note["page"] is not None:
                lines.extend([f"Страница: {note['page']}", ""])
            if note["text"]:
                lines.extend("> " + line for line in note["text"].splitlines())
                lines.append("")
            if note["note"]:
                lines.extend([note["note"], ""])
    return "\n".join(lines) + "\n"


def write_reports(source, output):
    now = datetime.now().astimezone()
    report = {"exported_at": now.isoformat(timespec="seconds"), "books": list(collect(source))}
    output.mkdir(parents=True, exist_ok=True)
    target = output / ("KOReader-" + now.strftime("%Y%m%d-%H%M%S-%f"))
    target.mkdir()
    (target / "Заметки.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (target / "Все заметки.md").write_text(markdown(report), encoding="utf-8")
    if report["books"]:
        summaries = target / "Конспекты"
        summaries.mkdir()
        for index, book in enumerate(report["books"], start=1):
            name = re.sub(r'[<>:"/\\|?*\x00-\x1f]+', " ", str(book["title"]))
            name = " ".join(name.split())[:68].strip(" .") or "Книга"
            chapters = {}
            for note in book["notes"]:
                chapters.setdefault(note["chapter"] or "Без главы", []).append(note)
            lines = [f"# {book['title']}", "", str(book["author"] or "Автор не указан"), "",
                     f"Выгружено {report['exported_at']}", ""]
            for chapter, group in chapters.items():
                lines.extend([f"## {chapter}", ""])
                for note in group:
                    meta = []
                    if note["page"] is not None:
                        meta.append(f"Страница {note['page']}")
                    if note["created_at_device"]:
                        meta.append(note["created_at_device"])
                    if meta:
                        lines.extend([" · ".join(meta), ""])
                    if note["text"]:
                        lines.extend("> " + line for line in note["text"].splitlines())
                        lines.append("")
                    if note["note"]:
                        lines.extend([note["note"], ""])
            (summaries / f"{index:03d} {name}.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    return target, len(report["books"])


def main():
    parser = argparse.ArgumentParser(description="Локальный экспорт заметок KOReader в JSON и Markdown")
    parser.add_argument("source", type=Path, help="папка Yandex Books или другая папка с EPUB и .sdr")
    parser.add_argument("--output", type=Path, default=Path.cwd(), help="папка на компьютере для выгрузки")
    args = parser.parse_args()
    if not args.source.is_dir():
        parser.error("папка с книгами не найдена")
    target, count = write_reports(args.source, args.output)
    print(f"Книг с заметками: {count}. Выгрузка: {target}")


if __name__ == "__main__":
    main()
