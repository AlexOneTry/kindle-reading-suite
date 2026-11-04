#!/usr/bin/env python3
"""Package an already built KUAL release, with its feature list and changes."""
from pathlib import Path
from xml.etree import ElementTree
from zipfile import ZIP_DEFLATED, ZipFile

ROOT = Path(__file__).resolve().parent


def main():
    extension = ROOT / "extension"
    version = ElementTree.parse(extension / "config.xml").findtext("information/version")
    if not version or not all(part.isdigit() for part in version.split(".")):
        raise SystemExit("Invalid extension version")
    changelog = ROOT / "CHANGELOG.md"
    features = ROOT / "FUNCTIONS.md"
    if version not in changelog.read_text(encoding="utf-8").splitlines()[0] or not features.stat().st_size:
        raise SystemExit("Update the feature list and changelog before packaging")
    output = ROOT / "dist" / f"Yandex-Books-Paperwhite-KUAL-{version}.zip"
    output.parent.mkdir(exist_ok=True)
    temporary = output.with_suffix(".tmp")
    with ZipFile(temporary, "w", ZIP_DEFLATED) as archive:
        for path in sorted(extension.rglob("*")):
            if path.is_file() and path.name not in {"pairing.txt", "last-run.log", ".DS_Store"}:
                archive.write(path, "extensions/yandex-books-pw2/" + path.relative_to(extension).as_posix())
        plugin = ROOT / "koreader-plugin" / "yandexbooksnotes.koplugin"
        for name in ("main.lua", "_meta.lua"):
            archive.write(plugin / name, f"koreader/plugins/{plugin.name}/{name}")
        for name, source in {
            "Инструкция.md": "README.md",
            "Функции.md": "FUNCTIONS.md",
            "Что нового.md": "CHANGELOG.md",
            "Подключить Kindle.command": "Подключить Kindle.command",
            "Connect-Kindle-Windows.cmd": "Connect-Kindle-Windows.cmd",
            "connect-kindle.ps1": "connect-kindle.ps1",
        }.items():
            archive.write(ROOT / source, name)
    with ZipFile(temporary) as archive:
        if archive.testzip() is not None:
            raise SystemExit("ZIP validation failed")
    temporary.replace(output)
    print(output)


if __name__ == "__main__":
    main()
