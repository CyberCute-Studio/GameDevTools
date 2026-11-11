package shell

import "sync"

type Dispatcher interface {
	Post(task func()) error
}

type dispatchTask struct {
	id  uint64
	run func()
}

type DispatchQueue struct {
	mu     sync.Mutex
	nextID uint64
	tasks  []dispatchTask
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
	q.nextID++
	id := q.nextID
	q.tasks = append(q.tasks, dispatchTask{id: id, run: task})
	q.mu.Unlock()
	if err := q.notify(); err != nil {
		q.mu.Lock()
		for i, queued := range q.tasks {
			if queued.id == id {
				q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
				break
			}
		}
		q.mu.Unlock()
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
		task.run()
	}
}
