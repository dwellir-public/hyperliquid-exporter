package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGetSet(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Set("k", "v")

	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if got != "v" {
		t.Fatalf("got %v, want v", got)
	}
}

func TestGetMissing(t *testing.T) {
	c := NewLRUCache(10, 0)
	got, ok := c.Get("nope")
	if ok || got != nil {
		t.Fatalf("expected miss, got %v %v", got, ok)
	}
}

func TestOverwrite(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Set("k", 1)
	c.Set("k", 2)

	got, _ := c.Get("k")
	if got != 2 {
		t.Fatalf("got %v, want 2", got)
	}
	if c.Len() != 1 {
		t.Fatalf("len %d, want 1", c.Len())
	}
}

func TestLRUEviction(t *testing.T) {
	c := NewLRUCache(2, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // evicts "a"

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("expected 'b' to survive")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected 'c' to survive")
	}
}

func TestLRUPromotion(t *testing.T) {
	c := NewLRUCache(2, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Get("a")    // promote a
	c.Set("c", 3) // evicts b (least recent)

	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected 'a' to survive after promotion")
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := NewLRUCache(10, 50*time.Millisecond)
	c.Set("k", "v")

	if _, ok := c.Get("k"); !ok {
		t.Fatal("expected hit before expiry")
	}

	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after TTL")
	}
	if c.Len() != 0 {
		t.Fatal("expired entry should be removed on access")
	}
}

func TestNoTTL(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Set("k", "v")
	time.Sleep(10 * time.Millisecond)

	if _, ok := c.Get("k"); !ok {
		t.Fatal("ttl=0 should never expire")
	}
}

func TestGetAllSkipsExpired(t *testing.T) {
	c := NewLRUCache(10, 50*time.Millisecond)
	c.Set("old", 1)
	time.Sleep(60 * time.Millisecond)
	c.Set("new", 2)

	all := c.GetAll()
	if _, ok := all["old"]; ok {
		t.Fatal("expired entry should be excluded from GetAll")
	}
	if _, ok := all["new"]; !ok {
		t.Fatal("fresh entry should be in GetAll")
	}
}

func TestCleanupExpired(t *testing.T) {
	c := NewLRUCache(10, 50*time.Millisecond)
	c.Set("a", 1)
	c.Set("b", 2)
	time.Sleep(60 * time.Millisecond)
	c.Set("c", 3) // fresh

	c.CleanupExpired()

	if c.Len() != 1 {
		t.Fatalf("len %d, want 1 after cleanup", c.Len())
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("fresh entry should survive cleanup")
	}
}

func TestCleanupNoTTL(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.CleanupExpired()

	if c.Len() != 2 {
		t.Fatal("CleanupExpired with ttl=0 should be a no-op")
	}
}

func TestDelete(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Set("k", "v")
	c.Delete("k")

	if _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after delete")
	}
	if c.Len() != 0 {
		t.Fatalf("len %d, want 0", c.Len())
	}
}

func TestDeleteMissing(t *testing.T) {
	c := NewLRUCache(10, 0)
	c.Delete("nope") // should not panic
}

func TestClear(t *testing.T) {
	c := NewLRUCache(10, 0)
	for i := range 5 {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("len %d after clear, want 0", c.Len())
	}
}

func TestConcurrency(t *testing.T) {
	c := NewLRUCache(100, time.Second)
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 100 {
				key := fmt.Sprintf("%d-%d", id, j)
				c.Set(key, j)
				c.Get(key)
			}
		}(i)
	}
	wg.Wait()
}
