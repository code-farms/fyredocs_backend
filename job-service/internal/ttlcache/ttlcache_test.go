package ttlcache

import (
	"sync"
	"testing"
	"time"
)

func TestGetSetDelete(t *testing.T) {
	c := New[string, int](time.Minute, 10)
	if _, ok := c.Get("missing"); ok {
		t.Error("expected miss on empty cache")
	}
	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("Get(a) = (%v,%v), want (1,true)", v, ok)
	}
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Error("expected miss after Delete")
	}
}

func TestExpiry(t *testing.T) {
	c := New[string, int](time.Minute, 10)
	now := time.Now()
	c.now = func() time.Time { return now }

	c.Set("a", 1)
	now = now.Add(59 * time.Second)
	if _, ok := c.Get("a"); !ok {
		t.Error("entry expired early")
	}
	now = now.Add(2 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Error("entry survived its TTL")
	}
	if c.Len() != 0 {
		t.Errorf("expired entry was not reaped on Get, len = %d", c.Len())
	}
}

func TestSizeCapEvictsExpiredFirst(t *testing.T) {
	c := New[int, int](time.Minute, 3)
	now := time.Now()
	c.now = func() time.Time { return now }

	c.Set(1, 1)
	c.Set(2, 2)
	now = now.Add(2 * time.Minute) // 1 and 2 are now expired
	c.Set(3, 3)
	c.Set(4, 4) // at capacity? no — expired ones get reaped when needed
	c.Set(5, 5)

	if c.Len() > 3 {
		t.Errorf("cache exceeded its cap: len = %d, want <= 3", c.Len())
	}
	// The live entries must survive.
	for _, k := range []int{3, 4, 5} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("live entry %d was evicted", k)
		}
	}
}

func TestSizeCapEvictsWhenAllLive(t *testing.T) {
	c := New[int, int](time.Hour, 2)
	c.Set(1, 1)
	c.Set(2, 2)
	c.Set(3, 3)
	if c.Len() > 2 {
		t.Errorf("cache exceeded its cap: len = %d, want <= 2", c.Len())
	}
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	c := New[int, int](time.Minute, 100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set(n, n)
			c.Get(n)
			if n%2 == 0 {
				c.Delete(n)
			}
		}(i)
	}
	wg.Wait()
}
