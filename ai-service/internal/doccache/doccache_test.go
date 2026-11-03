package doccache

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"ai-service/internal/ops"
)

func store(t *testing.T) (*Redis, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return &Redis{Client: c, TTL: time.Minute}, mr
}

var pages = []ops.Page{{Index: 0, Text: "one"}, {Index: 4, Text: "five"}}

func TestPutGetRoundTripAndStableRef(t *testing.T) {
	s, _ := store(t)
	ref, err := s.Put(t.Context(), "u1", pages)
	if err != nil || len(ref) != 64 {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
	again, _ := s.Put(t.Context(), "u1", pages)
	if again != ref {
		t.Fatal("identical documents must share a ref")
	}
	got, ok, err := s.Get(t.Context(), "u1", ref)
	if err != nil || !ok || len(got) != 2 || got[1] != pages[1] {
		t.Fatalf("got=%v ok=%v err=%v", got, ok, err)
	}
}

func TestRefsAreScopedPerUser(t *testing.T) {
	s, _ := store(t)
	ref, _ := s.Put(t.Context(), "u1", pages)
	if _, ok, _ := s.Get(t.Context(), "u2", ref); ok {
		t.Fatal("another user's ref must miss")
	}
}

func TestExpiryAndSlidingTTL(t *testing.T) {
	s, mr := store(t)
	ref, _ := s.Put(t.Context(), "u1", pages)
	mr.FastForward(50 * time.Second)
	if _, ok, _ := s.Get(t.Context(), "u1", ref); !ok {
		t.Fatal("hit expected before TTL")
	}
	mr.FastForward(50 * time.Second) // 100s since put, 50s since the sliding read
	if _, ok, _ := s.Get(t.Context(), "u1", ref); !ok {
		t.Fatal("a read must slide the TTL")
	}
	mr.FastForward(2 * time.Minute)
	if _, ok, err := s.Get(t.Context(), "u1", ref); ok || err != nil {
		t.Fatalf("expired entry: ok=%v err=%v", ok, err)
	}
}
