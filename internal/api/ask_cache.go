package api

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// askCache is a tiny LRU+TTL cache over /ask responses. Keyed on the canonical
// request signature (question + surface + limit). 5-minute TTL guards against
// stale answers when the user just wrote a new engram; 64-entry cap keeps memory
// bounded even on a long-running daemon under dashboard polling.
//
// Thread-safe via sync.Mutex. Sequential reads/writes are fine — /ask is
// rate-limited by SQLite's single-writer model anyway.
type askCache struct {
	mu       sync.Mutex
	max      int
	ttl      time.Duration
	order    *list.List               // front = most recently used
	entries  map[string]*list.Element // key → element holding *askEntry

	// Observability counters (v0.0.49+). Atomic so /metrics can read without
	// taking the cache mutex.
	hits   atomic.Uint64
	misses atomic.Uint64
}

type askEntry struct {
	key       string
	value     []byte // serialized JSON response
	expiresAt time.Time
}

func newAskCache(max int, ttl time.Duration) *askCache {
	return &askCache{
		max:     max,
		ttl:     ttl,
		order:   list.New(),
		entries: make(map[string]*list.Element, max),
	}
}

// Get returns (value, true) if the key is cached and not expired.
func (c *askCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	e := el.Value.(*askEntry)
	if time.Now().After(e.expiresAt) {
		c.order.Remove(el)
		delete(c.entries, key)
		c.misses.Add(1)
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return e.value, true
}

// Stats returns a snapshot of hit/miss counters + current size. Safe to call
// concurrently with Get/Put.
func (c *askCache) Stats() (hits, misses uint64, size int) {
	return c.hits.Load(), c.misses.Load(), c.Len()
}

// Put stores key→value with the cache TTL. Evicts LRU if over capacity.
func (c *askCache) Put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		// Refresh in-place.
		e := el.Value.(*askEntry)
		e.value = value
		e.expiresAt = time.Now().Add(c.ttl)
		c.order.MoveToFront(el)
		return
	}
	e := &askEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	el := c.order.PushFront(e)
	c.entries[key] = el
	if c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.entries, oldest.Value.(*askEntry).key)
		}
	}
}

// Len returns the current entry count (post-expiry sweep not applied — entries
// expire lazily on Get). Used in tests.
func (c *askCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
