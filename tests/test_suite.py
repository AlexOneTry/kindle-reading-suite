"""Release checks using synthetic books/accounts. No personal data or real login."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import sys
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch
from xml.etree import ElementTree as ET
from zipfile import ZipFile, ZIP_STORED

SUITE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SUITE))
import installer
import reading
import articles
import notes
import words
import connect_yandex


class ReleaseChecks(unittest.TestCase):
    def setUp(self):
        self.temp = TemporaryDirectory(prefix="Kindle tests русский ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        home = patch.object(installer.Path, "home", return_value=self.root)
        home.start()
        self.addCleanup(home.stop)
        self.kindle = self.root / "Kindle c пробелом"
        (self.kindle / "koreader").mkdir(parents=True)
        (self.kindle / "koreader/reader.lua").write_text("-- synthetic KOReader")

    def report(self, **changes):
        data = dict(schema=1, probe_version="1.0.0", runtime="arm", root=True,
                    touch=True, fbink_ok=True, width=758, height=1024, model="fixture")
        data.update(changes)
        path = self.kindle / installer.REPORT_PATH
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(data), encoding="utf-8")
        return data

    def test_package_integrity(self):
        installer.verify_payload()

    def test_account_callback_rejects_wrong_origin_state_and_duplicate(self):
        token = "y0_" + "test-token_"*5
        url = f"https://{connect_yandex.HOST}/callback#access_token={token}&state=example"
        self.assertEqual(connect_yandex.token_from_callback(url, "example"), token)
        for bad in [url.replace(connect_yandex.HOST, "attacker.example"), url+"&access_token="+token,
                    url+"&state=example", url+"&error=denied", url.replace("https:", "http:")]:
            with self.assertRaises(ValueError): connect_yandex.token_from_callback(bad, "example")
        with self.assertRaises(ValueError): connect_yandex.token_from_callback(url, "another")

    def test_pairing_writes_only_after_network_check(self):
        folder = self.kindle / "extensions/yandex-books-pw2"
        folder.mkdir(parents=True)
        (folder / "config.xml").write_text("<extension/>")
        token = "y0_" + "fixture_"*6
        url = f"https://{connect_yandex.HOST}/callback#access_token={token}&state=fixture-state"
        with patch.object(connect_yandex.secrets, "token_hex", return_value="fixture-state"), patch.object(connect_yandex.webbrowser, "open", return_value=True), patch.object(connect_yandex.getpass, "getpass", return_value=url), patch.object(connect_yandex, "check_access"):
            connect_yandex.pair(self.kindle)
        self.assertEqual((folder / "pairing.txt").read_text(), token+"\n")
        with patch.object(connect_yandex.secrets, "token_hex", return_value="fixture-state"), patch.object(connect_yandex.webbrowser, "open", return_value=True), patch.object(connect_yandex.getpass, "getpass", return_value=url), patch.object(connect_yandex, "check_access", side_effect=ValueError("offline")), self.assertRaises(ValueError):
            connect_yandex.pair(self.kindle)
        self.assertEqual((folder / "pairing.txt").read_text(), token+"\n")

    def test_windows_drive_detection_and_multiple_devices(self):
        self.assertEqual(reading.volume_roots("nt")[0], "D:\\")
        self.assertEqual(reading.volume_roots("nt")[-1], "Z:\\")
        self.assertEqual(reading.detect_kindles([self.root / "not a kindle", self.kindle]), [self.kindle])
        with patch.object(installer, "find_devices", return_value=[self.kindle, self.root]), patch("builtins.input", return_value="2"):
            self.assertEqual(installer.select_device(), self.root)

    def test_requires_runtime_not_model_name(self):
        self.assertFalse(installer.incompatibilities(self.report()))
        for bad in [{"touch":False}, {"root":False}, {"fbink_ok":False}, {"runtime":"arm64"},
                    {"width":1024, "height":758}, {"width":"758"}, {"probe_error":"old kernel"}]:
            self.assertTrue(installer.incompatibilities(self.report(**bad)))

    def test_first_step_only_installs_diagnostic(self):
        with patch.object(installer, "apply_files", wraps=installer.apply_files) as call, patch.object(installer.Path, "home", return_value=self.root):
            installer.install(self.kindle)
        self.assertTrue((self.kindle / "extensions/reading-kit-check/probe").is_file())
        self.assertFalse((self.kindle / "extensions/yandex-books-pw2/yandex-books").exists())

    def test_reject_unsupported_without_overwrite(self):
        self.report(touch=False)
        with self.assertRaises(ValueError): installer.install(self.kindle)
        self.assertFalse((self.kindle / "extensions/yandex-books-pw2").exists())

    def test_install_preserves_notes_session_and_is_idempotent(self):
        self.report()
        paths = ["Yandex Books/Книга.sdr/metadata.epub.lua", "extensions/yandex-books-pw2/pairing.txt"]
        for name in paths:
            p = self.kindle / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b"private fixture, do not replace")
        with patch.object(installer.Path, "home", return_value=self.root):
            installer.install(self.kindle)
            self.assertIsNone(installer.install(self.kindle))
        for name in paths:
            self.assertEqual((self.kindle / name).read_bytes(), b"private fixture, do not replace")

    def test_backup_and_rollback(self):
        file = self.kindle / "extensions/example/file"
        file.parent.mkdir(parents=True)
        file.write_bytes(b"original")
        original = installer.replace_file
        calls = [0]
        def fails_once(path, data):
            calls[0] += 1
            if calls[0] == 2: raise OSError("simulated disconnected device")
            return original(path, data)
        with patch.object(installer, "replace_file", side_effect=fails_once), self.assertRaises(OSError):
            installer.apply_files(self.kindle, {"extensions/example/file": b"new", "extensions/example/second":b"new"}, self.root / "backups")
        self.assertEqual(file.read_bytes(), b"original")
        self.assertFalse((file.parent / "second").exists())
        self.assertEqual(next((self.root / "backups").rglob("file")).read_bytes(), b"original")

    def test_traversal_and_symlink_refused(self):
        with self.assertRaises(ValueError): installer.apply_files(self.kindle, {"../escape": b"x"})
        if sys.platform != "win32":
            (self.kindle / "link").symlink_to(self.root, target_is_directory=True)
            with self.assertRaises(ValueError): installer.apply_files(self.kindle, {"link/escape": b"x"})

    def test_dictionary_installs_with_uncompressed_index(self):
        reading.install_dictionary(self.kindle)
        folder = self.kindle / "koreader/data/dict" / reading.DICTIONARY
        self.assertTrue(list(folder.glob("*.idx")))
        self.assertTrue(words.dictionary_lookup(folder, ["book"]).get("book"))
        reading.install_dictionary(self.kindle)

    def test_article_epub_xml_and_duplicate(self):
        html = "<html lang='en'><head><title>Test &amp; title</title></head><body><nav>IGNORE ME</nav><article><h1>Article</h1><h2>Chapter</h2>" + "<p>Useful &amp; safe content.</p>"*30 + "</article><script>BAD SCRIPT</script></body></html>"
        with patch.object(articles, "fetch", return_value=(html.encode(), "utf-8", "https://example.org/article")):
            path = articles.save_article("https://example.org/article", self.root)
            self.assertEqual(articles.save_article("https://example.org/article", self.root), path)
        with ZipFile(path) as epub:
            self.assertIsNone(epub.testzip())
            self.assertEqual(epub.infolist()[0].filename, "mimetype")
            self.assertEqual(epub.infolist()[0].compress_type, ZIP_STORED)
            for name in epub.namelist():
                if name.endswith((".xml", ".opf", ".xhtml")): ET.fromstring(epub.read(name))
            text = epub.read("OEBPS/article.xhtml").decode()
            self.assertIn("Useful", text)
            self.assertNotIn("IGNORE ME", text)
            self.assertNotIn("BAD SCRIPT", text)

    def test_rss_feed_fallback(self):
        feed = b'<rss><channel><item><title>Test</title><link>https://example.org/article</link><description>fallback</description></item></channel></rss>'
        self.assertEqual(next(articles.feed_entries(feed, "https://example.org"))[1], "Test")
        fallback = "<h1>Offline RSS</h1>" + "<p>Saved feed content for reading.</p>"*10
        with patch.object(articles, "fetch", side_effect=OSError("offline")):
            path = articles.save_article("https://example.org/rss-item", self.root, "RSS title", fallback)
        self.assertTrue(path.is_file())

    def test_notes_metadata_and_timestamps(self):
        article = articles.article_from_html("<article><h1>Книга</h1>"+"<p>Текст страницы.</p>"*40+"</article>", "https://example.org/book")
        book = articles.save_epub(article, self.kindle)
        sidecar = book.with_suffix(".sdr")
        sidecar.mkdir()
        (sidecar / "metadata.epub.lua").write_text('return { annotations = { { text = "Цитата", note = "Мой комментарий", chapter = "Глава 1", pageno = 7, datetime = "2026-11-04 10:15:00", pos0 = "a", pos1 = "b" } } }', encoding="utf-8")
        target, count = notes.write_reports(self.kindle, self.root / "export")
        data = json.loads((target / "Заметки.json").read_text(encoding="utf-8"))
        self.assertEqual(count, 1)
        self.assertTrue(data["exported_at"].endswith("+00:00") or "+" in data["exported_at"][10:] or "-" in data["exported_at"][19:])
        self.assertTrue(data["books"][0]["epub_metadata"])
        self.assertEqual(data["books"][0]["notes"][0]["created_at_device"], "2026-11-04 10:15:00")
        self.assertIn("Мой комментарий", next((target / "Конспекты").glob("*.md")).read_text(encoding="utf-8"))

    def test_sqlite_wal_included_and_original_unchanged(self):
        db = self.root / "vocabulary_builder.sqlite3"
        connection = sqlite3.connect(db)
        self.addCleanup(connection.close)
        connection.execute("PRAGMA journal_mode=WAL")
        connection.execute("PRAGMA wal_autocheckpoint=0")
        connection.execute("CREATE TABLE vocabulary(word TEXT, book_title TEXT, create_time INTEGER)")
        connection.commit()
        connection.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        connection.execute("INSERT INTO vocabulary VALUES('book', 'Fixture book', 1793780000)")
        connection.commit()
        wal = db.with_name(db.name+"-wal")
        before = [p.read_bytes() for p in (db, wal)]
        self.assertEqual(words.read_words(db)[0][0], "book")
        self.assertEqual(before, [p.read_bytes() for p in (db, wal)])
        target, count = words.export_words(db, None, self.root / "words")
        self.assertEqual(count, 1)
        self.assertIn("Fixture book", (target / "Карточки.html").read_text(encoding="utf-8"))

    def test_mtp_stage_is_local_and_checks_report(self):
        destination = self.root / "MTP files"
        installer.stage(None, destination)
        self.assertTrue((destination / "extensions/reading-kit-check/check.sh").is_file())
        self.report(runtime="arm64")
        with self.assertRaises(ValueError): installer.stage(self.kindle / installer.REPORT_PATH, destination)


if __name__ == "__main__": unittest.main()
