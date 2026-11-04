"""Save public articles and RSS/Atom entries as offline EPUBs."""
from datetime import datetime, timezone
from hashlib import sha256
from html import escape
from html.parser import HTMLParser
from pathlib import Path
import re
from tempfile import NamedTemporaryFile
from urllib.parse import urljoin, urlsplit
from urllib.request import Request, urlopen
from xml.etree import ElementTree as ET
from zipfile import ZipFile, ZIP_DEFLATED, ZIP_STORED

LIMIT = 8 * 1024 * 1024
ALLOWED = set("h1 h2 h3 h4 h5 h6 p blockquote pre code ul ol li dl dt dd em i strong b a br hr figure figcaption table thead tbody tr th td sup sub".split())
VOID = set("area base br col embed hr img input link meta param source track wbr".split())
IGNORED = set("script style noscript svg canvas form iframe dialog nav aside footer button template".split())
CONTROL = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f]")


def web_url(url):
    parts = urlsplit(url)
    if parts.scheme.lower() in {"https", "http"} and parts.hostname and not parts.username and not parts.password:
        return parts._replace(fragment="").geturl()
    raise ValueError("нужен адрес http:// или https://")


def fetch(url):
    request = Request(web_url(url), headers={"User-Agent": "KOReader-Reading-Kit/1.0", "Accept": "text/html, application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.1"})
    with urlopen(request, timeout=20) as reply:
        final = web_url(reply.geturl())
        data = reply.read(LIMIT + 1)
        charset = reply.headers.get_content_charset()
    if len(data) > LIMIT:
        raise ValueError("страница или RSS превышает 8 МБ")
    return data, charset, final


def decode_html(data, charset):
    if not charset:
        match = re.search(rb"charset\s*=\s*[\"']?([a-zA-Z0-9_-]+)", data[:4096], re.I)
        charset = match.group(1).decode("ascii") if match else "utf-8-sig"
    try:
        return data.decode(charset, errors="replace")
    except LookupError:
        return data.decode("utf-8-sig", errors="replace")


class Node:
    def __init__(self, tag, attrs=()):
        self.tag = tag
        self.attrs = {key: value or "" for key, value in attrs}
        self.children = []


class PageParser(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node("root")
        self.stack = [self.root]
        self.metadata = {}

    def handle_starttag(self, tag, attrs):
        node = Node(tag, attrs)
        if tag == "meta":
            key = node.attrs.get("property", node.attrs.get("name", "")).lower()
            if key:
                self.metadata.setdefault(key, node.attrs.get("content", ""))
        if tag in {"p", "li", "td", "th", "dt", "dd"} and self.stack[-1].tag == tag:
            self.stack.pop()
        self.stack[-1].children.append(node)
        if tag not in VOID and len(self.stack) < 200:
            self.stack.append(node)

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if tag not in VOID:
            self.handle_endtag(tag)

    def handle_endtag(self, tag):
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                return

    def handle_data(self, data):
        self.stack[-1].children.append(CONTROL.sub("", data))


def nodes(node):
    yield node
    if node.tag in IGNORED:
        return
    for child in node.children:
        if isinstance(child, Node):
            yield from nodes(child)


def render(node, url, headings=None):
    if isinstance(node, str):
        return escape(node), node
    if node.tag in IGNORED or node.tag == "head" or "hidden" in node.attrs or node.attrs.get("aria-hidden") == "true":
        return "", ""
    if node.attrs.get("role") in {"navigation", "complementary", "banner", "contentinfo"}:
        return "", ""
    parts = [render(child, url, headings) for child in node.children]
    body = "".join(p[0] for p in parts)
    text = " ".join(p[1] for p in parts)
    tag = node.tag
    if tag not in ALLOWED:
        return body, text
    attrs = ""
    if tag == "a" and node.attrs.get("href"):
        try:
            attrs = f' href="{escape(web_url(urljoin(url, node.attrs["href"])), quote=True)}"'
        except ValueError:
            pass
    if tag in {"br", "hr"}:
        return f"<{tag}/>", text
    if headings is not None and tag in {"h2", "h3"} and text.strip():
        anchor = f"section-{len(headings)+1}"
        attrs += f' id="{anchor}"'
        headings.append((anchor, " ".join(text.split())))
    return f"<{tag}{attrs}>{body}</{tag}>", text


def article_from_html(source, url, fallback="", allow_short=False):
    parser = PageParser()
    parser.feed(source)
    parsed = list(nodes(parser.root))
    candidates = [n for n in parsed if n.tag == "article" or n.attrs.get("role") == "article"]
    candidates += [n for n in parsed if n.tag == "main" or n.attrs.get("role") == "main"]
    fallback_node = next((n for n in parsed if n.tag == "body"), parser.root)
    # Prefer a substantial article. Short cards and navigation are common in main.
    content = None
    for group in (candidates[:24], [fallback_node]):
        best_score = 0
        for n in group:
            _, raw = render(n, url)
            score = len(" ".join(raw.split()))
            if score > best_score:
                best_score, content = score, n
        if best_score >= (60 if allow_short else 240):
            break
    if not content or best_score < (60 if allow_short else 240):
        raise ValueError("на странице не нашлось текста статьи")
    meta = parser.metadata
    heading = next((render(n, url)[1] for n in nodes(content) if n.tag == "h1"), "")
    page_title = next(("".join(x for x in n.children if isinstance(x, str)) for n in parsed if n.tag == "title"), "")
    title = " ".join((meta.get("og:title") or heading or fallback or page_title or "Статья").split())
    first_heading = next((n for n in nodes(content) if n.tag == "h1"), None)
    if first_heading and " ".join(heading.split()) == title:
        first_heading.tag = "span"
        first_heading.children = []
    headings = []
    body, _ = render(content, url, headings)
    if not any(tag in body for tag in ("<p>", "<h1>", "<div", "<blockquote>", "<li>")):
        body = "<p>" + body + "</p>"
    lang = next((n.attrs.get("lang", "") for n in parsed if n.tag == "html"), "")
    return {"title": title, "author": meta.get("author", ""), "lang": lang or "und",
            "published": meta.get("article:published_time", ""), "headings": headings,
            "body": body, "url": url}


def document(title, lang, body):
    return ('<?xml version="1.0" encoding="utf-8"?>\n'
            f'<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="{escape(lang, quote=True)}">'
            f'<head><title>{escape(title)}</title><link rel="stylesheet" href="style.css"/></head><body>{body}</body></html>')


def save_epub(article, target):
    url = article["url"]
    key = sha256(url.encode()).hexdigest()[:16]
    title = CONTROL.sub("", article["title"])
    name = re.sub(r'[<>:"/\\|?*]+', " ", title)
    name = " ".join(name.split())[:62].strip(" .") or "Статья"
    target.mkdir(parents=True, exist_ok=True)
    existing = next((p for p in target.iterdir() if p.name.endswith(f" [{key}].epub") and p.is_file()), None)
    if existing:
        return existing
    path = target / f"{name} [{key}].epub"
    lang = article["lang"]
    source = f'<p class="source"><a href="{escape(url, quote=True)}">Оригинал статьи</a></p>'
    byline = " · ".join(x for x in (article["author"], article["published"]) if x)
    header = f'<header><h1>{escape(title)}</h1>{f"<p>{escape(byline)}</p>" if byline else ""}</header>'
    nav = '<li><a href="article.xhtml">Статья</a></li>' + "".join(
        f'<li><a href="article.xhtml#{anchor}">{escape(text)}</a></li>' for anchor, text in article["headings"])
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    creator = f'<dc:creator>{escape(article["author"])}</dc:creator>' if article["author"] else ""
    opf = f'''<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">{escape(url)}</dc:identifier>
<dc:title>{escape(title)}</dc:title><dc:language>{escape(lang)}</dc:language>{creator}
<dc:source>{escape(url)}</dc:source><meta property="dcterms:modified">{now}</meta></metadata>
<manifest><item id="article" href="article.xhtml" media-type="application/xhtml+xml"/>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="style" href="style.css" media-type="text/css"/></manifest><spine><itemref idref="article"/></spine></package>'''
    css = "body{margin:4%;line-height:1.35}p{margin:.6em 0}h1{font-size:1.65em}h2{margin-top:1.3em}blockquote{margin:1em 1.5em}pre{white-space:pre-wrap}.source{font-size:.82em}"
    with NamedTemporaryFile(dir=target, suffix=".tmp", delete=False) as handle:
        tmp = Path(handle.name)
    try:
        with ZipFile(tmp, "w", ZIP_DEFLATED) as epub:
            epub.writestr("mimetype", "application/epub+zip", ZIP_STORED)
            epub.writestr("META-INF/container.xml", '<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
            epub.writestr("OEBPS/content.opf", opf)
            epub.writestr("OEBPS/article.xhtml", document(title, lang, header + article["body"] + source))
            epub.writestr("OEBPS/nav.xhtml", document(title, lang, f'<nav epub:type="toc"><h1>Оглавление</h1><ol>{nav}</ol></nav>'))
            epub.writestr("OEBPS/style.css", css)
        tmp.replace(path)
    finally:
        tmp.unlink(missing_ok=True)
    return path


def save_article(url, target, fallback="", feed_body=""):
    try:
        data, charset, final = fetch(url)
        article = article_from_html(decode_html(data, charset), final, fallback)
    except (OSError, ValueError):
        if not feed_body:
            raise
        article = article_from_html(feed_body, web_url(url), fallback, allow_short=True)
    return save_epub(article, target)


def feed_entries(data, base):
    root = ET.fromstring(data)
    def tag(n):
        return n.tag.rsplit("}", 1)[-1]
    for entry in root.iter():
        if tag(entry) not in {"item", "entry"}:
            continue
        title = body = href = ""
        for child in entry:
            kind = tag(child)
            if kind == "title":
                title = "".join(child.itertext()).strip()
            elif kind == "link" and child.get("rel", "alternate") == "alternate":
                href = child.get("href") or (child.text or "").strip()
            elif kind in {"encoded", "content", "description", "summary"}:
                text = "".join(ET.tostring(n, encoding="unicode") for n in child) if len(child) else child.text or ""
                if len(text) > len(body):
                    body = text
        if href:
            try:
                yield web_url(urljoin(base, href)), title, body
            except ValueError:
                continue
