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
	// Optional, for multi-step jobs such as creating a project.
	Steps []TaskStep
	Log   []string
	URL   string // shown as "Open" when the task is done
}

type TaskStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pending | running | done | failed | skipped
}

// taskLogLines is how much output a task keeps for the dashboard.
const taskLogLines = 60

type TaskView struct {
	ID       int        `json:"id"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Progress float64    `json:"progress"`
	Message  string     `json:"message"`
	Error    string     `json:"error,omitempty"`
	Started  int64      `json:"started"`
	Ended    int64      `json:"ended,omitempty"`
	Steps    []TaskStep `json:"steps,omitempty"`
	Log      []string   `json:"log,omitempty"`
	URL      string     `json:"url,omitempty"`
}

func (t *Task) Set(msg string, progress float64) {
	t.mu.Lock()
	t.Message, t.Progress = msg, progress
	t.mu.Unlock()
}

func (t *Task) View() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := TaskView{ID: t.ID, Title: t.Title, Status: t.Status, Progress: t.Progress, Message: t.Message, Error: t.Error,
		Started: t.Started.Unix(), Steps: append([]TaskStep(nil), t.Steps...), Log: append([]string(nil), t.Log...), URL: t.URL}
	if !t.Ended.IsZero() {
		v.Ended = t.Ended.Unix()
	}
	return v
}

// SetSteps declares the steps of the task, all pending.
func (t *Task) SetSteps(names []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Steps = make([]TaskStep, len(names))
	for i, n := range names {
		t.Steps[i] = TaskStep{Name: n, Status: "pending"}
	}
}

// StepStatus updates step i and moves the progress bar along.
func (t *Task) StepStatus(i int, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if i < 0 || i >= len(t.Steps) {
		return
	}
	t.Steps[i].Status = status
	if status == "running" {
		t.Message = t.Steps[i].Name
	}
	finished := 0
	for _, s := range t.Steps {
		if s.Status == "done" || s.Status == "skipped" {
			finished++
		}
	}
	t.Progress = float64(finished) / float64(len(t.Steps))
}

// AddLog appends a line of command output.
func (t *Task) AddLog(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Log = append(t.Log, line)
	if len(t.Log) > taskLogLines {
		t.Log = t.Log[len(t.Log)-taskLogLines:]
	}
}

func (t *Task) SetURL(u string) {
	t.mu.Lock()
	t.URL = u
	t.mu.Unlock()
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
			for i := range t.Steps {
				if t.Steps[i].Status == "running" {
					t.Steps[i].Status = "failed"
				}
			}
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
