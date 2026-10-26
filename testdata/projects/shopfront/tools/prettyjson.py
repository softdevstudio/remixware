"""Print the catalog as readable JSON."""


def to_text(obj, width=2, depth=0):
    """Format obj as indented JSON text."""
    pad = " " * (width * depth)
    inner = " " * (width * (depth + 1))
    if isinstance(obj, dict):
        if not obj:
            return "{}"
        parts = []
        for name in sorted(obj):
            text = to_text(obj[name], width, depth + 1)
            parts.append('%s"%s": %s' % (inner, quote(str(name)), text))
        return "{\n" + ",\n".join(parts) + "\n" + pad + "}"
    if isinstance(obj, (list, tuple)):
        if not obj:
            return "[]"
        parts = [inner + to_text(v, width, depth + 1) for v in obj]
        return "[\n" + ",\n".join(parts) + "\n" + pad + "]"
    if obj is None:
        return "null"
    if obj is True or obj is False:
        return "true" if obj else "false"
    if isinstance(obj, (int, float)):
        return repr(obj)
    return '"%s"' % quote(str(obj))


def quote(text):
    """Escape a string for JSON."""
    return text.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")
