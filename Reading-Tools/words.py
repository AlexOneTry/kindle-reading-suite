"""Read KOReader Vocabulary Builder locally and write Anki TSV and offline cards."""
import csv
from datetime import datetime, timezone
from contextlib import closing
import gzip
from html import escape
from html.parser import HTMLParser
from pathlib import Path
import shutil
import sqlite3
import struct
from tempfile import TemporaryDirectory


class Definition(HTMLParser):
    def __init__(self):
        super().__init__()
        self.parts = []
    def handle_data(self, data):
        self.parts.append(data)


def dictionary_lookup(folder, wanted):
    wanted = {word.casefold() for word in wanted}
    if not folder or not folder.is_dir():
        return {}
    for idx in folder.glob("*.idx"):
        ifo = idx.with_suffix(".ifo")
        compressed = idx.with_suffix(".dict.dz")
        plain = idx.with_suffix(".dict")
        if not ifo.is_file() or not (compressed.is_file() or plain.is_file()):
            continue
        info = ifo.read_text(encoding="utf-8", errors="replace")
        if "idxoffsetbits=64" in info:
            continue
        index = idx.read_bytes()
        defs = gzip.decompress(compressed.read_bytes()) if compressed.exists() else plain.read_bytes()
        entries = {}
        position = 0
        while position < len(index):
            end = index.find(b"\0", position)
            if end < 0 or end + 9 > len(index):
                break
            word = index[position:end].decode("utf-8", errors="replace").casefold()
            offset, size = struct.unpack_from(">II", index, end + 1)
            position = end + 9
            if word not in wanted or offset + size > len(defs):
                continue
            data = defs[offset:offset+size].decode("utf-8", errors="replace").replace("\x00", " ").strip()
            if "<" in data:
                parser = Definition()
                parser.feed(data)
                data = " ".join(parser.parts)
            text = " ".join(data.split()).strip()
            if text and text not in entries.setdefault(word, []):
                entries[word].append(text)
        return {key: "; ".join(value) for key, value in entries.items()}
    return {}


def read_words(db):
    if not db.is_file():
        raise ValueError("база слов не найдена. Добавь слово в «Словарь для запоминания» и выйди из KOReader.")
    # KOReader may use WAL. Work on a copy so SQLite can create its lock
    # files locally without needing write access to the Kindle USB volume.
    with TemporaryDirectory(prefix="koreader-vocab-") as folder:
        copy = Path(folder) / db.name
        shutil.copyfile(db, copy)
        wal = db.with_name(db.name + "-wal")
        if wal.is_file():
            shutil.copyfile(wal, copy.with_name(copy.name + "-wal"))
        with closing(sqlite3.connect(copy)) as connection:
            return _read_snapshot(connection)


def _read_snapshot(connection):
    cols = {r[1] for r in connection.execute("PRAGMA table_info(vocabulary)")}
    if "word" not in cols:
        raise ValueError("неизвестный формат базы слов KOReader")
    has_title = "title_id" in cols and connection.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='title'").fetchone()
    source = "title.name" if has_title else "vocabulary.book_title" if "book_title" in cols else "NULL"
    fields = ["prev_context", "highlight", "next_context", "create_time", "due_time"]
    projection = ", ".join(f'vocabulary.{n}' if n in cols else "NULL" for n in fields)
    join = " LEFT JOIN title ON title.id=vocabulary.title_id" if has_title else ""
    order = " ORDER BY create_time DESC" if "create_time" in cols else " ORDER BY word"
    return list(connection.execute(f"SELECT word, {source}, {projection} FROM vocabulary{join}{order}"))


def timestamp(value):
    if not value:
        return ""
    try:
        return datetime.fromtimestamp(value, timezone.utc).isoformat(timespec="seconds")
    except (ValueError, TypeError, OverflowError, OSError):
        return ""


def export_words(db, dictionary, output):
    rows = read_words(db)
    gloss = dictionary_lookup(dictionary, [r[0] for r in rows])
    output.mkdir(parents=True, exist_ok=True)
    now = datetime.now().astimezone()
    target = output / ("Слова-" + now.strftime("%Y%m%d-%H%M%S-%f"))
    target.mkdir()
    cards = []
    with (target / "Слова для Anki.tsv").open("w", encoding="utf-8", newline="") as file:
        file.write("#separator:Tab\n#html:false\n#columns:Слово\tПеревод\tКонтекст\tКнига\tДобавлено UTC\tПовторить UTC\n")
        writer = csv.writer(file, delimiter="\t")
        for word, book, before, highlight, after, created, due in rows:
            context = " ".join(x.strip() for x in (before, highlight or word, after) if x and x.strip())
            translation = gloss.get(word.casefold(), "")
            writer.writerow([word, translation, context, book or "", timestamp(created), timestamp(due)])
            cards.append(f"<details><summary>{escape(word)}</summary><p>{escape(translation or 'Нет точного совпадения в словаре')}</p>"
                         f"<p class='context'>{escape(context)}</p><p class='source'>{escape(book or '')}</p></details>")
    (target / "Карточки.html").write_text(
        '<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">'
        '<title>Слова KOReader</title><style>body{max-width:42rem;margin:2rem auto;padding:0 1rem;font:1.1rem system-ui}'
        'details{border:1px solid #aaa;border-radius:.4rem;padding:.8rem;margin:.7rem 0}summary{cursor:pointer;font-weight:700}'
        '.context{font-style:italic}.source{font-size:.85rem;color:#555}</style>'
        f'<h1>Слова для повторения</h1><p>Нажми на слово, чтобы увидеть перевод и контекст. Выгружено {escape(now.isoformat(timespec="seconds"))}.</p>'
        + ("".join(cards) or "<p>Список пока пуст.</p>") + "</html>", encoding="utf-8")
    (target / "Источник словаря.txt").write_text(
        "Англо-русские определения: FreeDict + WikDict, издатель Karl Bartel, "
        "данные Викисловаря через DBnary. CC BY-SA 3.0.\n"
        "https://freedict.org/downloads/\nhttps://creativecommons.org/licenses/by-sa/3.0/\n",
        encoding="utf-8")
    return target, len(rows)
