package abci

import (
	"testing"
	"time"
)

func TestCacheHit(t *testing.T) {
	c := NewCache()
	mt := time.Now()
	ctx := &ContextInfo{Height: 100}

	c.Set("/path", ctx, mt)
	got := c.Get("/path", mt)

	if got == nil {
		t.Fatal("expected cache hit")
	}
	if got.Height != 100 {
		t.Errorf("Height = %d, want 100", got.Height)
	}
}

func TestCacheMissEmpty(t *testing.T) {
	c := NewCache()
	if got := c.Get("/nope", time.Now()); got != nil {
		t.Fatal("expected nil for empty cache")
	}
}

func TestCacheModtimeInvalidation(t *testing.T) {
	c := NewCache()
	t1 := time.Now()
	t2 := t1.Add(time.Second)

	c.Set("/path", &ContextInfo{Height: 1}, t1)
	got := c.Get("/path", t2)

	if got != nil {
		t.Fatal("expected nil when modtime changed")
	}
	// stale entry should be deleted
	if c.Get("/path", t1) != nil {
		t.Fatal("stale entry should have been deleted")
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := NewCache()
	mt := time.Now()

	c.Set("/path", &ContextInfo{Height: 1}, mt)
	c.Set("/path", &ContextInfo{Height: 2}, mt)

	got := c.Get("/path", mt)
	if got == nil || got.Height != 2 {
		t.Fatalf("expected Height=2 after overwrite, got %v", got)
	}
}

func TestCacheClear(t *testing.T) {
	c := NewCache()
	mt := time.Now()
	c.Set("/path", &ContextInfo{Height: 1}, mt)
	c.Clear()

	if got := c.Get("/path", mt); got != nil {
		t.Fatal("expected nil after clear")
	}
}

func TestCacheCleanupExpired(t *testing.T) {
	c := NewCache()
	c.CleanupExpired() // should not panic on empty cache
}
