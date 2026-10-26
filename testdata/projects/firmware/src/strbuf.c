/* Growable buffers for the console log. */
#include <stdlib.h>
#include <string.h>
#include "log.h"

int logbuf_grow(struct logbuf *lb, size_t extra)
{
    size_t need = lb->len + extra + 1;
    if (need <= lb->cap)
        return 0;
    size_t cap = lb->cap ? lb->cap : 16;
    while (cap < need)
        cap *= 2;
    char *p = realloc(lb->buf, cap);
    if (p == NULL)
        return -1;
    lb->buf = p;
    lb->cap = cap;
    return 0;
}

int logbuf_append(struct logbuf *lb, const char *s, size_t n)
{
    if (logbuf_grow(lb, n) != 0)
        return -1;
    memcpy(lb->buf + lb->len, s, n);
    lb->len += n;
    lb->buf[lb->len] = '\0';
    return 0;
}

void logbuf_reset(struct logbuf *lb)
{
    lb->len = 0;
    if (lb->buf)
        lb->buf[0] = '\0';
}
