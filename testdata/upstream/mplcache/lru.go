// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package mplcache is a small least-recently-used cache.
package mplcache

import "container/list"

type entry struct {
	key   string
	value any
}

// LRU keeps at most Size entries and drops the least recently used one.
type LRU struct {
	size  int
	order *list.List
	items map[string]*list.Element
	evict func(key string, value any)
}

// New returns an empty cache for size entries. evict, if set, is called
// for every entry the cache drops.
func New(size int, evict func(key string, value any)) *LRU {
	return &LRU{size: size, order: list.New(), items: make(map[string]*list.Element), evict: evict}
}

// Get returns the value for key and marks it as recently used.
func (c *LRU) Get(key string) (any, bool) {
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry).value, true
}

// Put stores value under key, dropping the oldest entry when the cache is full.
func (c *LRU) Put(key string, value any) {
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).value = value
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&entry{key: key, value: value})
	c.items[key] = el
	if c.order.Len() > c.size {
		last := c.order.Back()
		c.order.Remove(last)
		e := last.Value.(*entry)
		delete(c.items, e.key)
		if c.evict != nil {
			c.evict(e.key, e.value)
		}
	}
}
