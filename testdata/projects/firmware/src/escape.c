/* Escapes text for the board's JSON status page. Written for this project. */
#include <stddef.h>

size_t json_escape(char *dst, size_t size, const char *src)
{
    size_t n = 0;
    for (; *src; src++) {
        char c = *src;
        const char *esc = 0;
        if (c == '"') esc = "\\\"";
        else if (c == '\\') esc = "\\\\";
        else if (c == '\n') esc = "\\n";
        else if (c == '\r') esc = "\\r";
        else if (c == '\t') esc = "\\t";
        if (esc) {
            if (n + 2 >= size) break;
            dst[n++] = esc[0];
            dst[n++] = esc[1];
        } else {
            if (n + 1 >= size) break;
            dst[n++] = c;
        }
    }
    if (size > 0) dst[n] = '\0';
    return n;
}
