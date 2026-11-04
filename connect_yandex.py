"""Account pairing for a USB disk or a local folder prepared for MTP copying."""
import getpass
import json
from pathlib import Path
import re
import secrets
from tempfile import NamedTemporaryFile
from urllib.parse import parse_qs, urlencode, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, Request
import webbrowser

CLIENT = "4483e97bab6e486a9822973109a14d05"
HOST = f"yx{CLIENT}.oauth.yandex.ru"


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def token_from_callback(address, state):
    try:
        uri = urlsplit(address.strip())
        if uri.scheme != "https" or uri.hostname != HOST or uri.username or uri.password or uri.port not in (None, 443):
            raise ValueError()
        fields = parse_qs(uri.fragment + "&" + uri.query, keep_blank_values=True)
        token = fields.get("access_token", [])
        if fields.get("state") != [state] or "error" in fields or len(token) != 1:
            raise ValueError()
        if not re.fullmatch(r"y0_[A-Za-z0-9_.~-]{17,4093}", token[0]):
            raise ValueError()
        return token[0]
    except (ValueError, TypeError):
        raise ValueError("вставь полный адрес страницы из вкладки, которую открыл этот вход") from None


def check_access(token):
    request = Request("https://api.bookmate.yandex.net/api/v5/profile/library_cards?limit=1&offset=0", headers={
        "Auth-Token": token, "App-Language":"ru", "App-Locale":"ru", "App-Platform":"android",
        "Bookmate-Version":"20200305", "Device-Os":"Android", "User-Agent":"okhttp/4.12.0"})
    try:
        with build_opener(NoRedirect).open(request, timeout=15) as response:
            if response.status != 200:
                raise ValueError()
            data = json.loads(response.read(2 * 1024 * 1024))
            if not isinstance(data, (dict, list)):
                raise ValueError()
    except (OSError, ValueError):
        raise ValueError("не удалось подтвердить доступ к полке. Проверь интернет и повтори вход") from None


def pair(root):
    folder = root / "extensions/yandex-books-pw2"
    if not (folder / "config.xml").is_file():
        raise ValueError("сначала установи приложение или подготовь папку MTP")
    state = secrets.token_hex(24)
    url = "https://oauth.yandex.ru/authorize?" + urlencode(dict(response_type="token", client_id=CLIENT, state=state))
    if not webbrowser.open(url):
        print("Открой в браузере: " + url)
    print("Войди в Яндекс. Ошибка открытия «Букмейта» после входа возможна.")
    print("Скопируй ПОЛНЫЙ адрес этой страницы и вставь сюда. Ввод скрыт. Адрес никому не передавай.")
    token = token_from_callback(getpass.getpass("Адрес: "), state)
    print("Проверяю доступ к полке…")
    check_access(token)
    with NamedTemporaryFile(dir=folder, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write((token + "\n").encode("ascii"))
    try:
        temporary.replace(folder / "pairing.txt")
    finally:
        temporary.unlink(missing_ok=True)
    print("Вход сохранён. Для MTP перенеси pairing.txt на Kindle, затем удали локальный pairing.txt.")
    print("Извлеки Kindle и запусти KUAL → Yandex Books.")
