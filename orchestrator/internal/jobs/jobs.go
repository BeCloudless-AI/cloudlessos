// Package jobs tracks asynchronous install operations and fans out progress
// updates to subscribers (e.g. SSE clients).
package jobs

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Update is a snapshot of a job's progress.
type Update struct {
	Phase       string `json:"phase"` // pending | pulling | starting | running | error
	Message     string `json:"message"`
	LayersDone  int    `json:"layersDone"`
	LayersTotal int    `json:"layersTotal"`
	BytesDone   int64  `json:"bytesDone,omitempty"`
	BytesTotal  int64  `json:"bytesTotal,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
	Error       string `json:"error,omitempty"`
	Done        bool   `json:"done"`
}

// Snapshot identifies a job together with its latest progress update.
type Snapshot struct {
	ID    string `json:"id"`
	AppID string `json:"appId"`
	Update
}

// Job is a single install operation and its subscribers.
type Job struct {
	ID    string
	AppID string

	mu    sync.Mutex
	state Update
	subs  map[chan Update]struct{}
}

// Manager owns all jobs.
type Manager struct {
	mu   sync.Mutex
	seq  int
	jobs map[string]*Job
}

// NewManager returns an empty job manager.
func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*Job)}
}

// Create registers a new pending job for an app.
func (m *Manager) Create(appID string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	j := &Job{
		ID:    fmt.Sprintf("job-%d", m.seq),
		AppID: appID,
		subs:  make(map[chan Update]struct{}),
		state: Update{Phase: "pending", Message: "Queued"},
	}
	m.jobs[j.ID] = j
	return j
}

// Get looks up a job by id.
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// List returns snapshots whose application id starts with prefix.
func (m *Manager) List(prefix string) []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.jobs))
	for _, j := range m.jobs {
		if prefix != "" && !strings.HasPrefix(j.AppID, prefix) {
			continue
		}
		out = append(out, Snapshot{ID: j.ID, AppID: j.AppID, Update: j.Snapshot()})
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out
}

// Snapshot returns the current state.
func (j *Job) Snapshot() Update {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// Subscribe returns a buffered channel pre-loaded with the current state.
func (j *Job) Subscribe() chan Update {
	j.mu.Lock()
	defer j.mu.Unlock()
	ch := make(chan Update, 16)
	ch <- j.state
	j.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe removes and closes a subscriber channel exactly once.
func (j *Job) Unsubscribe(ch chan Update) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.subs[ch]; ok {
		delete(j.subs, ch)
		close(ch)
	}
}

// apply mutates the state and broadcasts it to subscribers (non-blocking).
func (j *Job) apply(fn func(*Update)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.state)
	for ch := range j.subs {
		select {
		case ch <- j.state:
		default: // slow subscriber: drop intermediate update, it'll get the next one
		}
	}
}

// Progress reports pull/start progress. Pass done/total < 0 to leave them unchanged.
func (j *Job) Progress(phase, msg string, done, total int) {
	j.apply(func(u *Update) {
		u.Phase = phase
		u.Message = msg
		if done >= 0 {
			u.LayersDone = done
		}
		if total >= 0 {
			u.LayersTotal = total
		}
	})
}

// ProgressBytes reports byte-level progress for downloads.
func (j *Job) ProgressBytes(phase, msg string, done, total int64) {
	j.apply(func(u *Update) {
		u.Phase = phase
		u.Message = msg
		u.BytesDone = done
		u.BytesTotal = total
	})
}

// Succeed marks the job as running (terminal success).
func (j *Job) Succeed(containerID string) {
	j.apply(func(u *Update) {
		u.Phase = "running"
		u.Message = "Running"
		u.ContainerID = containerID
		u.LayersDone = u.LayersTotal
		u.Done = true
	})
}

// Fail marks the job as errored (terminal failure).
func (j *Job) Fail(err error) {
	j.apply(func(u *Update) {
		u.Phase = "error"
		u.Message = "Failed"
		u.Error = err.Error()
		u.Done = true
	})
}

// Cancel marks the job as deliberately stopped by the user.
func (j *Job) Cancel() {
	j.apply(func(u *Update) {
		u.Phase = "canceled"
		u.Message = "Canceled"
		u.Error = ""
		u.Done = true
	})
}
