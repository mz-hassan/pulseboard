package boardcache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/example/pulseboard/internal/store"
)

type fakeStore struct {
	mu    sync.Mutex
	loads int
	ready chan struct{}
}

func (f *fakeStore) CreateRoom(context.Context, string, string) error       { return nil }
func (f *fakeStore) Authorize(context.Context, string, string) error        { return nil }
func (f *fakeStore) SaveStroke(context.Context, string, store.Stroke) error { return nil }
func (f *fakeStore) Clear(context.Context, string) error                    { return nil }
func (f *fakeStore) Ping(context.Context) error                             { return nil }
func (f *fakeStore) LoadStrokes(context.Context, string) ([]store.Stroke, error) {
	f.mu.Lock()
	f.loads++
	f.mu.Unlock()
	if f.ready != nil {
		<-f.ready
	}
	return []store.Stroke{{ID: "one"}}, nil
}

func TestStateCoalescesColdLoads(t *testing.T) {
	f := &fakeStore{ready: make(chan struct{})}
	c := New(f, time.Minute, 10, 1<<20)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.State(context.Background(), "room"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	close(f.ready)
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loads != 1 {
		t.Fatalf("loads = %d, want 1", f.loads)
	}
}

func TestAppendUpdatesCachedState(t *testing.T) {
	c := New(&fakeStore{}, time.Minute, 10, 1<<20)
	if _, _, err := c.State(context.Background(), "room"); err != nil {
		t.Fatal(err)
	}
	c.Append("room", store.Stroke{ID: "two"})
	payload, hit, err := c.State(context.Background(), "room")
	if err != nil || !hit || string(payload) == "" || !contains(string(payload), "two") {
		t.Fatalf("hit=%v err=%v payload=%s", hit, err, payload)
	}
}

func TestCacheHonorsMemoryBudget(t *testing.T) {
	f := &fakeStore{}
	c := New(f, time.Minute, 10, 1)
	if _, hit, err := c.State(context.Background(), "room"); err != nil || hit {
		t.Fatalf("first load hit=%v err=%v", hit, err)
	}
	if _, hit, err := c.State(context.Background(), "room"); err != nil || hit {
		t.Fatalf("oversized payload must not be cached; hit=%v err=%v", hit, err)
	}
}

func contains(s, part string) bool { return len(s) >= len(part) && (s == part || index(s, part) >= 0) }
func index(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
