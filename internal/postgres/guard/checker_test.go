package guard

import (
	"sync"
	"testing"
)

func TestCheckerCachesAcceptedVerdict(t *testing.T) {
	c := NewChecker(8)
	first, err := c.Check("SELECT id FROM public.users WHERE active = $1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Check("SELECT id FROM public.users WHERE active = $1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("expected the cached *Result to be reused")
	}
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}
}

func TestCheckerDoesNotCacheRejections(t *testing.T) {
	c := NewChecker(8)
	if _, err := c.Check("DELETE FROM public.t"); err == nil {
		t.Fatal("expected rejection")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 (rejections must not be cached)", c.Len())
	}
}

func TestCheckerEvictsBeyondBound(t *testing.T) {
	c := NewChecker(2)
	for _, sql := range []string{"SELECT 1", "SELECT 2", "SELECT 3"} {
		if _, err := c.Check(sql); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
}

func TestCheckerConcurrent(t *testing.T) {
	c := NewChecker(16)
	sql := "SELECT * FROM public.t WHERE id = $1"
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Check(sql); err != nil {
				t.Errorf("check: %v", err)
			}
		}()
	}
	wg.Wait()
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}
}
