package shell

import "sync"

type Dispatcher interface {
	Post(task func()) error
}

type DispatchQueue struct {
	mu     sync.Mutex
	tasks  []func()
	notify func() error
}

func NewDispatchQueue(notify func() error) *DispatchQueue {
	return &DispatchQueue{notify: notify}
}

func (q *DispatchQueue) Post(task func()) error {
	if task == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, task)
	if err := q.notify(); err != nil {
		q.tasks = q.tasks[:len(q.tasks)-1]
		return err
	}
	return nil
}

func (q *DispatchQueue) Drain() {
	q.mu.Lock()
	tasks := q.tasks
	q.tasks = nil
	q.mu.Unlock()
	for _, task := range tasks {
		task()
	}
}
