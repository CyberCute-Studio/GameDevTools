package imageresizer

import (
	"context"
	"fmt"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

// Inspection is one queued source content check.
type Inspection struct {
	ID   uint64
	Path string
}

// InspectionResult is the outcome of one content check: the decoded
// source's description, or the typed decode failure.
type InspectionResult struct {
	ID   uint64
	Info decode.Info
	Err  error
}

// inspectFunc matches decode.Inspect so tests can substitute a controllable
// fake.
type inspectFunc func(context.Context, string) (decode.Info, error)

// Inspector checks source content on one background goroutine, one file at
// a time: the GUI thread never decodes (TECH-005), and at most one source is
// decoded in memory at once (NFR-014). The goroutine starts when work is
// queued and exits when the queue is empty. Each finished check is handed to
// deliver on the worker goroutine; the view forwards it to the GUI thread
// through the shell dispatcher. A check that is dropped or cancelled is
// never delivered.
//
// Enqueue, Drop, DropAll, and Close are safe to call from any goroutine.
type Inspector struct {
	inspect inspectFunc
	deliver func(InspectionResult)

	mu      sync.Mutex
	queue   []Inspection
	running bool
	closed  bool
	// current and cancelCurrent identify the in-flight check so Drop can
	// abandon it promptly.
	current       uint64
	cancelCurrent context.CancelFunc

	ctx     context.Context
	stop    context.CancelFunc
	workers sync.WaitGroup
}

func NewInspector(inspect inspectFunc, deliver func(InspectionResult)) *Inspector {
	ctx, stop := context.WithCancel(context.Background())
	return &Inspector{inspect: inspect, deliver: deliver, ctx: ctx, stop: stop}
}

// Enqueue appends checks in order. It does nothing once the inspector is
// closed.
func (i *Inspector) Enqueue(items []Inspection) {
	if len(items) == 0 {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return
	}
	i.queue = append(i.queue, items...)
	if !i.running {
		i.running = true
		i.workers.Add(1)
		go i.run()
	}
}

// Drop abandons the checks for the given item identifiers: queued checks
// are removed, and an in-flight one is cancelled at its next read.
func (i *Inspector) Drop(ids []uint64) {
	if len(ids) == 0 {
		return
	}
	dropping := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		dropping[id] = struct{}{}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	kept := i.queue[:0]
	for _, item := range i.queue {
		if _, drop := dropping[item.ID]; !drop {
			kept = append(kept, item)
		}
	}
	clear(i.queue[len(kept):])
	i.queue = kept
	if _, drop := dropping[i.current]; drop && i.cancelCurrent != nil {
		i.cancelCurrent()
	}
}

// DropAll abandons every queued and in-flight check.
func (i *Inspector) DropAll() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.queue = nil
	if i.cancelCurrent != nil {
		i.cancelCurrent()
	}
}

// Close abandons all work and refuses new checks. It does not wait; Wait
// blocks until the worker goroutine has exited.
func (i *Inspector) Close() {
	i.mu.Lock()
	i.closed = true
	i.queue = nil
	i.mu.Unlock()
	i.stop()
}

// Wait blocks until the worker goroutine, if any, has exited. Callers close
// the inspector first so the worker does not start further checks.
func (i *Inspector) Wait() {
	i.workers.Wait()
}

func (i *Inspector) run() {
	defer i.workers.Done()
	for {
		i.mu.Lock()
		if i.closed || len(i.queue) == 0 {
			i.running = false
			i.mu.Unlock()
			return
		}
		next := i.queue[0]
		i.queue[0] = Inspection{}
		i.queue = i.queue[1:]
		ctx, cancel := context.WithCancel(i.ctx)
		i.current, i.cancelCurrent = next.ID, cancel
		i.mu.Unlock()

		info, err := i.check(ctx, next.Path)
		abandoned := ctx.Err() != nil
		cancel()

		i.mu.Lock()
		i.current, i.cancelCurrent = 0, nil
		i.mu.Unlock()
		if !abandoned {
			i.deliver(InspectionResult{ID: next.ID, Info: info, Err: err})
		}
	}
}

// resultMailbox hands finished checks from the worker goroutine to the GUI
// thread in batches. At most one drain task is outstanding at a time, so a
// burst of quick checks costs one dispatcher message — and one shell relayout
// — per GUI turn rather than one per file, and can never fill the GUI
// thread's message queue. It is safe for concurrent use.
type resultMailbox struct {
	mu      sync.Mutex
	pending []InspectionResult
	posted  bool
}

// put queues a result and reports whether the caller must post a drain task:
// true only when none is outstanding.
func (m *resultMailbox) put(result InspectionResult) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, result)
	if m.posted {
		return false
	}
	m.posted = true
	return true
}

// postFailed records that the drain task could not be posted. The queued
// results are kept, and the next put posts again.
func (m *resultMailbox) postFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.posted = false
}

// take removes and returns every queued result, in arrival order; the next
// put posts a new drain task.
func (m *resultMailbox) take() []InspectionResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := m.pending
	m.pending, m.posted = nil, false
	return results
}

// check runs one inspection, containing a panic as that item's failure so
// one source can never stop the remaining checks (NFR-021). decode.Inspect
// recovers its own decoder panics; this guard sits at the goroutine
// boundary for whatever inspect function the inspector runs, because a
// panic escaping the worker would end the whole process.
func (i *Inspector) check(ctx context.Context, path string) (info decode.Info, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			info = decode.Info{}
			err = &decode.Error{Kind: decode.KindCorrupt, Err: fmt.Errorf("decoder failure: %v", recovered)}
		}
	}()
	return i.inspect(ctx, path)
}
