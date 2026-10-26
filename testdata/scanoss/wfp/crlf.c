/* Ring buffer for the tests: written for Remixware, not copied. */
#include <stdlib.h>

typedef struct Ring {
    int *items;
    size_t head, tail, size;
} Ring;

Ring *ring_new(size_t size) {
    Ring *r = malloc(sizeof(Ring));
    if (!r) return NULL;
    r->items = calloc(size, sizeof(int));
    r->head = r->tail = 0;
    r->size = size;
    return r;
}

int ring_push(Ring *r, int v) {
    size_t next = (r->head + 1) % r->size;
    if (next == r->tail) return -1;
    r->items[r->head] = v;
    r->head = next;
    return 0;
}

int ring_pop(Ring *r, int *v) {
    if (r->head == r->tail) return -1;
    *v = r->items[r->tail];
    r->tail = (r->tail + 1) % r->size;
    return 0;
}
