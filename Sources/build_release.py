#!/usr/bin/env python3
"""Build a distribution from an explicit allowlist, excluding personal data."""
import hashlib
from html import escape
import json
from pathlib import Path
import re
import shutil
from zipfile import ZipFile, ZIP_DEFLATED

ROOT = Path(__file__).resolve().parent
WORK = ROOT.parent
VERSION = "1.0.0"
NAME = f"Kindle-Reading-Suite-{VERSION}"


def readable_html(documents):
    def inline(text):
        text = escape(text)
        text = re.sub(r"\[([^\]]+)\]\((https?://[^\s)]+)\)", r'<a href="\2">\1</a>', text)
        text = re.sub(r"`([^`]+)`", r"<code>\1</code>", text)
        return re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)
    navigation, content = [], []
    for index, (title, source) in enumerate(documents):
        navigation.append(f'<a href="#doc{index}">{escape(title)}</a>')
        blocks = [f'<section id="doc{index}">']
        in_code = False
        code = []
        for line in source.splitlines():
            if line.startswith("```"):
                if in_code:
                    blocks.append("<pre><code>"+escape("\n".join(code))+"</code></pre>")
                    code = []
                in_code = not in_code
            elif in_code:
                code.append(line)
            elif line.startswith("#"):
                level = min(len(line)-len(line.lstrip("#")), 4)
                blocks.append(f'<h{level}>{inline(line.lstrip("# "))}</h{level}>')
            elif re.match(r"^\|[\s:|-]+\|$", line):
                continue
            elif line.startswith("|"):
                cells = line.strip("|").split("|")
                blocks.append('<div class="row">'+"".join('<span>'+inline(cell.strip())+'</span>' for cell in cells)+'</div>')
            elif line.startswith("- ") or re.match(r"^\d+\. ", line):
                blocks.append('<p class="item">'+inline(line)+'</p>')
            elif line.strip():
                blocks.append('<p>'+inline(line)+'</p>')
        blocks.append("</section>")
        content.append("\n".join(blocks))
    return ('<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">'
            '<title>Kindle Reading Suite — инструкции</title><style>'
            'body{font:17px/1.65 system-ui,sans-serif;color:#222;background:#faf9f5;margin:0}'
            'nav,main{max-width:900px;margin:auto;padding:28px}nav{display:flex;gap:12px;flex-wrap:wrap;border-bottom:1px solid #ddd}'
            'a{color:#155a78}h1,h2,h3{line-height:1.2}section{padding:24px 0 40px;border-bottom:1px solid #ddd;scroll-margin-top:20px}'
            'pre{overflow:auto;padding:18px;background:#eee;border-radius:8px}code{font-size:.88em}.item{padding-left:14px}'
            '.row{display:flex;gap:16px;border-bottom:1px solid #ddd;padding:8px 0}.row span{flex:1;min-width:0;overflow-wrap:anywhere}'
            '@media print{nav{display:none}section{break-before:page}body{background:white;font-size:11pt}}</style>'
            '<nav>'+"".join(navigation)+'</nav><main>'+"".join(content)+'</main></html>')


def main():
    stage = ROOT / "dist" / NAME
    # Only our disposable build folder may be replaced.
    if stage.exists(): shutil.rmtree(stage)
    stage.mkdir(parents=True)
    def copy(source, relative):
        dest = stage / relative
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, dest)
    for name in ["installer.py", "connect_yandex.py", "Install-Mac.command", "Install-Windows.cmd",
                 "Verify-Mac.command", "Verify-Windows.cmd", "НАЧНИ ЗДЕСЬ.md", "FUNCTIONS.md", "CHANGELOG.md", "TEST-REPORT.md"]:
        copy(ROOT / name, name)
    for path in (ROOT / "docs").glob("*.md"): copy(path, "Инструкции/" + path.name)
    for path in (ROOT / "tests").glob("*.py"): copy(path, "tests/" + path.name)
    for name in ["reading.py", "articles.py", "notes.py", "words.py", "KOReader Tools.command",
                 "KOReader-Tools-Windows.cmd", "2-reading-kit-russian.lua", "README.md", "FUNCTIONS.md", "CHANGELOG.md"]:
        copy(WORK / "kindle-reading-kit" / name, "Reading-Tools/" + name)
    for path in (WORK / "kindle-reading-kit/dictionary").iterdir():
        if path.is_file(): copy(path, "Reading-Tools/dictionary/" + path.name)
    archive = WORK / "kindle-yandex/dist/Yandex-Books-Paperwhite-KUAL-0.6.0.zip"
    with ZipFile(archive) as source:
        for entry in source.infolist():
            rel = Path(entry.filename)
            if rel.is_absolute() or ".." in rel.parts: raise ValueError("unsafe release archive")
            dest = stage / "Yandex-Books" / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(source.read(entry))
            if entry.filename.endswith((".command", ".sh", "/fbink", "/yandex-books")): dest.chmod(0o755)
    for name in ["probe", "check.sh", "config.xml", "menu.json"]:
        copy(ROOT / "compatibility-extension" / name, "Compatibility-Check/" + name)
    copy(WORK / "kindle-yandex/extension/fbink", "Compatibility-Check/fbink")
    for path in (WORK / "kindle-yandex/extension/licenses").glob("*"):
        copy(path, "Compatibility-Check/licenses/" + path.name)
    copy(WORK / "kindle-yandex/extension/THIRD-PARTY.txt", "Compatibility-Check/THIRD-PARTY.txt")
    for path in (WORK / "kindle-yandex").glob("*.go"): copy(path, "Sources/Yandex-Books/" + path.name)
    for name in ["go.mod", "go.sum", "catalog-query.graphql", "README.md", "package_release.py"]:
        copy(WORK / "kindle-yandex" / name, "Sources/Yandex-Books/" + name)
    for name in ["start.sh", "menu.json", "config.xml"]:
        copy(WORK / "kindle-yandex/extension" / name, "Sources/Yandex-Books/extension/" + name)
    for path in (ROOT / "probe").glob("*.go"): copy(path, "Sources/Compatibility-Probe/" + path.name)
    for filename, relative in [("/tmp/fbink-corresponding-source.tar.gz", "FBInk-25c83924-source.tar.gz"),
                               ("/tmp/hotfix-build-source.tar.gz", "Hotfix-2.5.0-build-source.tar.gz")]:
        copy(Path(filename), "Sources/Third-Party/" + relative)
    copy(ROOT / "build_release.py", "Sources/build_release.py")
    documents = [("Начало", (ROOT / "НАЧНИ ЗДЕСЬ.md").read_text(encoding="utf-8"))]
    documents.extend((p.stem, p.read_text(encoding="utf-8")) for p in sorted((ROOT / "docs").glob("*.md")))
    documents.extend((label, (ROOT / name).read_text(encoding="utf-8")) for label, name in [
        ("Функции", "FUNCTIONS.md"), ("Что нового", "CHANGELOG.md"), ("Проверки", "TEST-REPORT.md")])
    (stage / "НАЧНИ ЗДЕСЬ.html").write_text(readable_html(documents), encoding="utf-8")
    for path in [stage / "Install-Mac.command", stage / "Compatibility-Check/check.sh", stage / "Compatibility-Check/probe"]:
        path.chmod(0o755)
    hashes = {p.relative_to(stage).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in sorted(stage.rglob("*")) if p.is_file()}
    (stage / "MANIFEST.json").write_text(json.dumps(hashes, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
    output = ROOT / "dist" / (NAME + ".zip")
    with ZipFile(output.with_suffix(".tmp"), "w", ZIP_DEFLATED) as archive:
        for path in sorted(stage.rglob("*")):
            if path.is_file(): archive.write(path, NAME + "/" + path.relative_to(stage).as_posix())
    with ZipFile(output.with_suffix(".tmp")) as archive:
        if archive.testzip(): raise ValueError("ZIP integrity failure")
    output.with_suffix(".tmp").replace(output)
    output.with_suffix(".zip.sha256").write_text(hashlib.sha256(output.read_bytes()).hexdigest()+"  "+output.name+"\n", encoding="ascii")
    print(stage)
    print(output)


if __name__ == "__main__": main()
