package imageresizer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// collector gathers delivered results for assertions.
type collector struct {
	mu      sync.Mutex
	results []InspectionResult
	arrived chan struct{}
}

func newCollector() *collector {
	return &collector{arrived: make(chan struct{}, 64)}
}

func (c *collector) deliver(result InspectionResult) {
	c.mu.Lock()
	c.results = append(c.results, result)
	c.mu.Unlock()
	c.arrived <- struct{}{}
}

func (c *collector) wait(t *testing.T, count int) []InspectionResult {
	t.Helper()
	for {
		c.mu.Lock()
		if len(c.results) >= count {
			results := append([]InspectionResult(nil), c.results...)
			c.mu.Unlock()
			return results
		}
		c.mu.Unlock()
		select {
		case <-c.arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d inspection results arrived", len(c.results), count)
		}
	}
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.results)
}

func TestInspectorDeliversInOrderAndGoesIdle(t *testing.T) {
	results := newCollector()
	inspector := NewInspector(func(_ context.Context, path string) (decode.Info, error) {
		if path == "bad" {
			return decode.Info{}, &decode.Error{Kind: decode.KindCorrupt, Err: errors.New("damaged")}
		}
		return decode.Info{Format: decode.FormatPNG, Width: len(path), Height: 1}, nil
	}, results.deliver)

	inspector.Enqueue([]Inspection{{ID: 1, Path: "a"}, {ID: 2, Path: "bad"}, {ID: 3, Path: "ccc"}})
	got := results.wait(t, 3)
	if got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("results out of order: %+v", got)
	}
	if got[0].Err != nil || got[0].Info.Width != 1 || got[2].Info.Width != 3 {
		t.Fatalf("successful results = %+v, %+v", got[0], got[2])
	}
	if got[1].Err == nil {
		t.Fatal("failed check was delivered without its error")
	}
	// The worker exits once the queue is empty, and a later Enqueue starts
	// a fresh one.
	inspector.Wait()
	inspector.Enqueue([]Inspection{{ID: 4, Path: "dd"}})
	if got := results.wait(t, 4); got[3].ID != 4 {
		t.Fatalf("result after idle = %+v", got[3])
	}
	inspector.Close()
	inspector.Wait()
}

// blockingInspect blocks each check until its context ends or release is
// closed, reporting each started path on started.
type blockingInspect struct {
	started chan string
	release chan struct{}
}

func newBlockingInspect() *blockingInspect {
	return &blockingInspect{started: make(chan string, 16), release: make(chan struct{})}
}

func (b *blockingInspect) inspect(ctx context.Context, path string) (decode.Info, error) {
	b.started <- path
	select {
	case <-ctx.Done():
		return decode.Info{}, &decode.Error{Kind: decode.KindCanceled, Err: ctx.Err()}
	case <-b.release:
		return decode.Info{Format: decode.FormatJPEG, Width: 1, Height: 1}, nil
	}
}

func (b *blockingInspect) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case path := <-b.started:
		return path
	case <-time.After(5 * time.Second):
		t.Fatal("no check started")
		return ""
	}
}

// TestInspectorDropAbandonsQueuedAndInFlightChecks pins that removing items
// stops their checks: the in-flight one is cancelled, queued ones never
// start, and neither is delivered.
func TestInspectorDropAbandonsQueuedAndInFlightChecks(t *testing.T) {
	results := newCollector()
	fake := newBlockingInspect()
	inspector := NewInspector(fake.inspect, results.deliver)
	inspector.Enqueue([]Inspection{{ID: 1, Path: "first"}, {ID: 2, Path: "second"}, {ID: 3, Path: "third"}})
	if path := fake.waitStarted(t); path != "first" {
		t.Fatalf("first started check = %q", path)
	}
	inspector.Drop([]uint64{1, 2})
	if path := fake.waitStarted(t); path != "third" {
		t.Fatalf("check after dropping = %q, want third", path)
	}
	close(fake.release)
	got := results.wait(t, 1)
	inspector.Close()
	inspector.Wait()
	if len(got) != 1 || got[0].ID != 3 || results.count() != 1 {
		t.Fatalf("delivered = %+v, want only item 3", got)
	}
}

func TestInspectorDropAllAndClose(t *testing.T) {
	results := newCollector()
	fake := newBlockingInspect()
	inspector := NewInspector(fake.inspect, results.deliver)
	inspector.Enqueue([]Inspection{{ID: 1, Path: "first"}, {ID: 2, Path: "second"}})
	fake.waitStarted(t)
	inspector.DropAll()
	inspector.Wait()
	if results.count() != 0 {
		t.Fatalf("DropAll delivered %d results", results.count())
	}

	inspector.Enqueue([]Inspection{{ID: 3, Path: "third"}})
	fake.waitStarted(t)
	inspector.Close()
	inspector.Wait()
	inspector.Enqueue([]Inspection{{ID: 4, Path: "fourth"}})
	inspector.Wait()
	select {
	case path := <-fake.started:
		t.Fatalf("closed inspector started %q", path)
	default:
	}
	if results.count() != 0 {
		t.Fatalf("closed inspector delivered %d results", results.count())
	}
}

// TestInspectorContainsPanics pins NFR-021: a check that panics becomes that
// item's failure, and the remaining checks still run.
func TestInspectorContainsPanics(t *testing.T) {
	results := newCollector()
	inspector := NewInspector(func(_ context.Context, path string) (decode.Info, error) {
		if path == "explode" {
			panic("decoder bug")
		}
		return decode.Info{Format: decode.FormatPNG, Width: 2, Height: 2}, nil
	}, results.deliver)
	inspector.Enqueue([]Inspection{{ID: 1, Path: "explode"}, {ID: 2, Path: "fine"}})
	got := results.wait(t, 2)
	inspector.Close()
	inspector.Wait()
	var decodeErr *decode.Error
	if !errors.As(got[0].Err, &decodeErr) || decodeErr.Kind != decode.KindCorrupt {
		t.Fatalf("panicking check = %+v, want a KindCorrupt error", got[0])
	}
	if got[1].Err != nil || got[1].ID != 2 {
		t.Fatalf("check after the panic = %+v", got[1])
	}
}

// TestResultMailboxPostsOncePerDrain pins the hand-off the view relies on:
// only the first result after a drain asks for a post, a refused post is
// retried by the next result, and nothing queued is ever lost.
func TestResultMailboxPostsOncePerDrain(t *testing.T) {
	var mailbox resultMailbox
	if !mailbox.put(InspectionResult{ID: 1}) {
		t.Fatal("the first result did not ask for a post")
	}
	if mailbox.put(InspectionResult{ID: 2}) {
		t.Fatal("a second result asked for a post while one is outstanding")
	}
	mailbox.postFailed()
	if !mailbox.put(InspectionResult{ID: 3}) {
		t.Fatal("the result after a refused post did not ask for a post")
	}
	got := mailbox.take()
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("take = %+v, want results 1, 2, 3 in order", got)
	}
	if rest := mailbox.take(); len(rest) != 0 {
		t.Fatalf("second take = %+v, want none", rest)
	}
	if !mailbox.put(InspectionResult{ID: 4}) {
		t.Fatal("the first result after a drain did not ask for a post")
	}
}
