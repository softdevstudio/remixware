"""Build URL slugs for product pages."""

import re
import unicodedata

_STRIP = re.compile(r"[^\w\s-]")
_DASHES = re.compile(r"[-\s]+")


def slugify(title, max_length=60):
    """Turn a page title into a URL path segment."""
    value = unicodedata.normalize("NFKD", title)
    value = value.encode("ascii", "ignore").decode("ascii")
    value = _STRIP.sub("", value).strip().lower()
    value = _DASHES.sub("-", value)
    if len(value) > max_length:
        cut = value.rfind("-", 0, max_length)
        value = value[: cut if cut > 0 else max_length]
    return value or "page"


def escape_html(text, quote=True):
    """Escape the characters that matter in HTML."""
    out = []
    for ch in text:
        if ch == "&":
            out.append("&amp;")
        elif ch == "<":
            out.append("&lt;")
        elif ch == ">":
            out.append("&gt;")
        elif quote and ch == '"':
            out.append("&quot;")
        else:
            out.append(ch)
    return "".join(out)


if __name__ == "__main__":
    import sys

    for line in sys.stdin:
        print(slugify(line.strip()))
