package main

import (
	"sort"
	"sync"
	"time"
)

// Task is a long-running background job (downloads/installs) shown in the dashboard.
type Task struct {
	mu       sync.Mutex
	ID       int
	Title    string
	Status   string // running | done | error
	Progress float64
	Message  string
	Error    string
	Started  time.Time
	Ended    time.Time
}

type TaskView struct {
	ID       int     `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Error    string  `json:"error,omitempty"`
	Started  int64   `json:"started"`
}

func (t *Task) Set(msg string, progress float64) {
	t.mu.Lock()
	t.Message, t.Progress = msg, progress
	t.mu.Unlock()
}

func (t *Task) View() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TaskView{t.ID, t.Title, t.Status, t.Progress, t.Message, t.Error, t.Started.Unix()}
}

type TaskManager struct {
	mu    sync.Mutex
	next  int
	tasks map[int]*Task
}

func NewTaskManager() *TaskManager { return &TaskManager{tasks: map[int]*Task{}} }

func (m *TaskManager) Run(title string, fn func(t *Task) error) *Task {
	m.mu.Lock()
	m.next++
	t := &Task{ID: m.next, Title: title, Status: "running", Progress: -1, Started: time.Now()}
	m.tasks[t.ID] = t
	m.mu.Unlock()
	go func() {
		err := fn(t)
		t.mu.Lock()
		t.Ended = time.Now()
		if err != nil {
			t.Status, t.Error = "error", err.Error()
		} else {
			t.Status, t.Progress = "done", 1
			if t.Message == "" {
				t.Message = "Done"
			}
		}
		t.mu.Unlock()
	}()
	return t
}

func (m *TaskManager) Get(id int) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// List returns running tasks plus those finished in the last 10 minutes.
func (m *TaskManager) List() []TaskView {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TaskView
	for id, t := range m.tasks {
		t.mu.Lock()
		old := !t.Ended.IsZero() && time.Since(t.Ended) > 10*time.Minute
		t.mu.Unlock()
		if old {
			delete(m.tasks, id)
			continue
		}
		out = append(out, t.View())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func (m *TaskManager) Busy(title string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		t.mu.Lock()
		busy := t.Title == title && t.Status == "running"
		t.mu.Unlock()
		if busy {
			return true
		}
	}
	return false
}
