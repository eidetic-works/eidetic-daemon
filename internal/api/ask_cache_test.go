package api

import (
	"testing"
	"time"
)

func TestAskCache_GetMiss(t *testing.T) {
	c := newAskCache(8, 1*time.Minute)
	if _, ok := c.Get("nope"); ok {
		t.Error("empty cache should miss")
	}
}

func TestAskCache_PutThenGet(t *testing.T) {
	c := newAskCache(8, 1*time.Minute)
	c.Put("k", []byte("v"))
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("should hit after Put")
	}
	if string(got) != "v" {
		t.Errorf("value: got %q, want v", got)
	}
}

func TestAskCache_TTLExpiry(t *testing.T) {
	c := newAskCache(8, 5*time.Millisecond)
	c.Put("k", []byte("v"))
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Error("entry should have expired")
	}
	if c.Len() != 0 {
		t.Errorf("expired entry should be removed; Len=%d", c.Len())
	}
}

func TestAskCache_LRUEviction(t *testing.T) {
	c := newAskCache(3, 1*time.Minute)
	c.Put("a", []byte("1"))
	c.Put("b", []byte("2"))
	c.Put("c", []byte("3"))
	// Touch "a" to make it most recent → "b" becomes LRU
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be cached")
	}
	c.Put("d", []byte("4")) // should evict "b"

	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted (LRU)")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a should still be cached (most recently used before put d)")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("c should still be cached")
	}
	if _, ok := c.Get("d"); !ok {
		t.Error("d should be cached (just put)")
	}
}

func TestAskCache_PutRefreshesExisting(t *testing.T) {
	c := newAskCache(8, 1*time.Minute)
	c.Put("k", []byte("v1"))
	c.Put("k", []byte("v2"))
	got, _ := c.Get("k")
	if string(got) != "v2" {
		t.Errorf("re-Put should refresh value; got %q", got)
	}
	if c.Len() != 1 {
		t.Errorf("re-Put should not grow Len; got %d", c.Len())
	}
}

func TestAskCache_StatsCounters(t *testing.T) {
	c := newAskCache(8, 1*time.Minute)
	h0, m0, s0 := c.Stats()
	if h0 != 0 || m0 != 0 || s0 != 0 {
		t.Errorf("fresh cache stats: got h=%d m=%d s=%d, want 0,0,0", h0, m0, s0)
	}

	// 3 misses
	c.Get("a")
	c.Get("b")
	c.Get("c")
	// 1 put + 1 hit
	c.Put("d", []byte("v"))
	c.Get("d")
	// 1 more miss
	c.Get("e")

	h, m, s := c.Stats()
	if h != 1 {
		t.Errorf("hits: got %d, want 1", h)
	}
	if m != 4 {
		t.Errorf("misses: got %d, want 4", m)
	}
	if s != 1 {
		t.Errorf("size: got %d, want 1", s)
	}
}
