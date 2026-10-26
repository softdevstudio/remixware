// Package cache keeps recently viewed products in memory.
package cache

import "container/list"

type item struct {
	key   string
	value any
}

// Products keeps at most size products and drops the least recently used.
type Products struct {
	size  int
	order *list.List
	items map[string]*list.Element
	evict func(key string, value any)
}

// NewProducts returns an empty cache.
func NewProducts(size int, evict func(key string, value any)) *Products {
	return &Products{size: size, order: list.New(), items: make(map[string]*list.Element), evict: evict}
}

// Get returns the value for key and marks it as recently used.
func (c *Products) Get(key string) (any, bool) {
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*item).value, true
}

// Put stores value under key, dropping the oldest entry when full.
func (c *Products) Put(key string, value any) {
	if el, ok := c.items[key]; ok {
		el.Value.(*item).value = value
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&item{key: key, value: value})
	c.items[key] = el
	if c.order.Len() > c.size {
		last := c.order.Back()
		c.order.Remove(last)
		e := last.Value.(*item)
		delete(c.items, e.key)
		if c.evict != nil {
			c.evict(e.key, e.value)
		}
	}
}
