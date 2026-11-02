package cache

import (
	"sync"
	"testing"
)

func TestUpdateCreatesAndUpdates(t *testing.T) {
	values := New[string, int](16)
	first := values.Update("counter", func(value int, exists bool) int {
		if exists || value != 0 {
			t.Fatalf("missing entry: got value=%d exists=%t", value, exists)
		}
		return 7
	})
	if first != 7 {
		t.Fatalf("first update returned %d, want 7", first)
	}
	second := values.Update("counter", func(value int, exists bool) int {
		if !exists || value != 7 {
			t.Fatalf("existing entry: got value=%d exists=%t", value, exists)
		}
		return value + 12
	})
	if value, exists := values.Get("counter"); !exists || value != 19 || second != 19 {
		t.Fatalf("updated entry: got value=%d exists=%t result=%d, want 19", value, exists, second)
	}
}

// 初始化与累加并发交错时，零值初始化不得覆盖已累计的值。
func TestUpdateConcurrentWithGetOrSet(t *testing.T) {
	values := New[int, int](16)
	const workers, increments = 16, 2000
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < increments; i++ {
				values.GetOrSet(1, 0)
				values.Update(1, func(value int, _ bool) int { return value + 1 })
			}
		}()
	}
	close(start)
	wg.Wait()
	if got, exists := values.Get(1); !exists || got != workers*increments {
		t.Fatalf("concurrent updates: got %d exists=%t, want %d", got, exists, workers*increments)
	}
}
