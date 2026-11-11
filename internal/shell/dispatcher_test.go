package shell

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDispatchQueueRunsTasksInOrderOnDrain(t *testing.T) {
	notifications := 0
	queue := NewDispatchQueue(func() error {
		notifications++
		return nil
	})
	var got []int
	for i := 0; i < 3; i++ {
		value := i
		if err := queue.Post(func() { got = append(got, value) }); err != nil {
			t.Fatalf("Post() error = %v", err)
		}
	}
	if len(got) != 0 {
		t.Fatal("task ran before Drain")
	}
	queue.Drain()
	if want := []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("execution order = %v, want %v", got, want)
	}
	if notifications != 3 {
		t.Fatalf("notifications = %d, want 3", notifications)
	}
	queue.Drain()
	if len(got) != 3 {
		t.Fatal("empty Drain reran a task")
	}
}

func TestDispatchQueueNotificationFailureRemovesTask(t *testing.T) {
	notifyErr := errors.New("window unavailable")
	queue := NewDispatchQueue(func() error { return notifyErr })
	ran := false
	if err := queue.Post(func() { ran = true }); !errors.Is(err, notifyErr) {
		t.Fatalf("Post() error = %v, want notification error", err)
	}
	queue.Drain()
	if ran {
		t.Fatal("task ran after failed notification")
	}
}

func TestDispatchQueueAcceptsNilTask(t *testing.T) {
	notified := false
	queue := NewDispatchQueue(func() error {
		notified = true
		return nil
	})
	if err := queue.Post(nil); err != nil {
		t.Fatalf("Post(nil) error = %v", err)
	}
	queue.Drain()
	if notified {
		t.Fatal("Post(nil) sent a notification")
	}
}

func TestDispatchQueueConcurrentPost(t *testing.T) {
	queue := NewDispatchQueue(func() error { return nil })
	const count = 100
	var ran atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := queue.Post(func() { ran.Add(1) }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	queue.Drain()
	if got := ran.Load(); got != count {
		t.Fatalf("executed tasks = %d, want %d", got, count)
	}
}
