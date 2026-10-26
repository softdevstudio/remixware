/* strbuf: a growable string buffer. See LICENSE for terms. */
#include <stdlib.h>
#include <string.h>
#include "strbuf.h"

int strbuf_grow(struct strbuf *sb, size_t extra)
{
    size_t need = sb->len + extra + 1;
    if (need <= sb->cap)
        return 0;
    size_t cap = sb->cap ? sb->cap : 16;
    while (cap < need)
        cap *= 2;
    char *p = realloc(sb->buf, cap);
    if (p == NULL)
        return -1;
    sb->buf = p;
    sb->cap = cap;
    return 0;
}

int strbuf_append(struct strbuf *sb, const char *s, size_t n)
{
    if (strbuf_grow(sb, n) != 0)
        return -1;
    memcpy(sb->buf + sb->len, s, n);
    sb->len += n;
    sb->buf[sb->len] = '\0';
    return 0;
}

int strbuf_append_escaped(struct strbuf *sb, const char *s)
{
    for (; *s; s++) {
        const char *rep = NULL;
        switch (*s) {
        case '"': rep = "\\\""; break;
        case '\\': rep = "\\\\"; break;
        case '\n': rep = "\\n"; break;
        case '\t': rep = "\\t"; break;
        }
        if (rep) {
            if (strbuf_append(sb, rep, 2) != 0)
                return -1;
        } else if (strbuf_append(sb, s, 1) != 0) {
            return -1;
        }
    }
    return 0;
}

void strbuf_free(struct strbuf *sb)
{
    free(sb->buf);
    sb->buf = NULL;
    sb->len = sb->cap = 0;
}
