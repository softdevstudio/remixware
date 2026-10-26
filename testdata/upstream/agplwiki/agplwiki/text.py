"""Text helpers for page titles and markup."""

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


def render_table(rows, header=True):
    """Render a list of rows as an HTML table."""
    if not rows:
        return ""
    lines = ["<table>"]
    for index, row in enumerate(rows):
        tag = "th" if header and index == 0 else "td"
        cells = "".join("<%s>%s</%s>" % (tag, escape_html(str(c)), tag) for c in row)
        lines.append("  <tr>%s</tr>" % cells)
    lines.append("</table>")
    return "\n".join(lines)
