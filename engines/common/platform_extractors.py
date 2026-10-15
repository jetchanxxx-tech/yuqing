"""Platform article extraction; scripts and navigation never become evidence."""
from html.parser import HTMLParser
import re

MAX_BODY_BYTES = 8 * 1024 * 1024
MAX_TEXT_CHARS = 200_000
_SELECTORS = {
    "wechat": ("#js_content", "article"),
    "toutiao": ("article", ".article-content", ".pgc-article"),
    "weibo": (".WB_text", "[data-testid='post-content']", "article", "main"),
    "douyin": ("[data-e2e='video-desc']", ".video-description", "article", "main"),
    "xigua": (".video-description", ".abstract", "article", "main"),
    "news": ("article", ".article-content", "main"),
}
_SKIP_TAGS = {"script", "style", "nav", "footer", "header", "aside", "noscript", "template", "svg", "form"}
_VOID_TAGS = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"}
_NOISE = re.compile(r"(?:^|[\s_-])(?:nav|footer|header|recommend(?:ation)?s?|related|advert(?:isement)?|ads?|comments?|login)(?:$|[\s_-])", re.I)
_BLOCKS = {"article", "section", "div", "p", "li", "h1", "h2", "h3", "h4", "br"}


def body_selectors(source_type):
    """The same article/description nodes define extraction and readiness."""
    return _SELECTORS.get(source_type, _SELECTORS["news"])


class _CleanText(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.stack = []
        self.parts = []
        self.skip = 0

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        hidden = tag in _SKIP_TAGS or bool(_NOISE.search(attrs.get("class", "") + " " + attrs.get("id", "")))
        hidden = hidden or attrs.get("aria-hidden") == "true" or "hidden" in attrs
        if tag not in _VOID_TAGS:
            self.stack.append((tag, hidden))
            self.skip += int(hidden)
        if not self.skip and tag in _BLOCKS:
            self.parts.append("\n")

    def handle_endtag(self, tag):
        for index in range(len(self.stack) - 1, -1, -1):
            if self.stack[index][0] == tag:
                self.skip -= sum(int(hidden) for _, hidden in self.stack[index:])
                del self.stack[index:]
                break
        if not self.skip and tag in _BLOCKS:
            self.parts.append("\n")

    def handle_data(self, data):
        if not self.skip:
            self.parts.append(data)


def clean_fragment(html):
    parser = _CleanText()
    parser.feed(str(html))
    return "\n".join(line for value in "".join(parser.parts).splitlines() if (line := re.sub(r"\s+", " ", value).strip()))


def _first(page, selectors):
    for selector in selectors:
        values = page.css(selector).getall()
        for value in values:
            if str(value).strip():
                return str(value).strip()
    return ""


def extract_page(page, source_type):
    """Return clean text and page metadata, with explicit size/integrity facts."""
    raw = getattr(page, "body", b"") or b""
    size = len(raw.encode("utf-8")) if isinstance(raw, str) else len(raw)
    if size > MAX_BODY_BYTES:
        return {"content": "", "reason": "response_body_limit", "truncated": True, "published_at": ""}
    fragments = []
    for selector in body_selectors(source_type):
        selected = page.css(selector)
        if selected:
            fragments = [str(node.html_content) for node in selected]
            if any(clean_fragment(fragment) for fragment in fragments):
                break
    if not fragments:
        # A bounded cleaned body is a fallback only, never script textContent.
        fragments = [str(node.html_content) for node in page.css("body")]
    text = "\n".join(clean_fragment(fragment) for fragment in fragments).strip()
    shell = re.search(r"(?:enable javascript|react app|vue app|安全验证|人机验证|请完成验证|访问过于频繁|请登录|登录后(?:查看|访问))", text, re.I)
    if shell and len(text) < 500:
        text = ""
    published_values = []
    for selector in ('meta[property="article:published_time"]::attr(content)', 'meta[name="publishdate"]::attr(content)',
                     'meta[name="pubdate"]::attr(content)', 'time::attr(datetime)'):
        published_values.extend(str(value).strip()[:100] for value in page.css(selector).getall() if str(value).strip())
    return {
        "content": text[:MAX_TEXT_CHARS], "truncated": len(text) > MAX_TEXT_CHARS,
        "reason": "verification_or_shell" if shell and not text else "" if text else "empty_body",
        "published_at": published_values[0] if published_values else "", "published_candidates": published_values,
        "title": _first(page, ("h1::text", "title::text", 'meta[property="og:title"]::attr(content)')),
        "author": _first(page, ('meta[name="author"]::attr(content)', ".author::text", ".byline::text")),
    }
