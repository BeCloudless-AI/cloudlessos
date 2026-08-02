// Package jobs tracks asynchronous install operations and fans out progress
// updates to subscribers (e.g. SSE clients).
package jobs

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Update is a snapshot of a job's progress.
type Update struct {
	Phase       string         `json:"phase"` // pending | pulling | starting | running | error
	Message     string         `json:"message"`
	LayersDone  int            `json:"layersDone"`
	LayersTotal int            `json:"layersTotal"`
	BytesDone   int64          `json:"bytesDone,omitempty"`
	BytesTotal  int64          `json:"bytesTotal,omitempty"`
	ContainerID string         `json:"containerId,omitempty"`
	Error       string         `json:"error,omitempty"`
	Done        bool           `json:"done"`
	Percent     int            `json:"percent"`
	ItemsDone   int            `json:"itemsDone,omitempty"`
	ItemsTotal  int            `json:"itemsTotal,omitempty"`
	CurrentItem string         `json:"currentItem,omitempty"`
	StartedAt   string         `json:"startedAt,omitempty"`
	UpdatedAt   string         `json:"updatedAt,omitempty"`
	ElapsedSecs int64          `json:"elapsedSeconds,omitempty"`
	ETASecs     int64          `json:"etaSeconds,omitempty"`
	Nodes       []NodeProgress `json:"nodes,omitempty"`
	etaOverride int64
}

type NodeProgress struct {
	Node       string `json:"node"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	BytesDone  int64  `json:"bytesDone,omitempty"`
	BytesTotal int64  `json:"bytesTotal,omitempty"`
	Percent    int    `json:"percent,omitempty"`
	ETASecs    int64  `json:"etaSeconds,omitempty"`
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
	seq   int

	mu       sync.Mutex
	state    Update
	subs     map[chan Update]struct{}
	start    time.Time
	observer func(Update)
}

// Manager owns all jobs.
type Manager struct {
	mu          sync.Mutex
	seq         int
	jobs        map[string]*Job
	maxTerminal int
}

// NewManager returns an empty job manager.
func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*Job), maxTerminal: 200}
}

// Create registers a new pending job for an app.
func (m *Manager) Create(appID string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(appID)
}

// CreateUnique atomically reconnects to a matching active job or creates one.
// The boolean is true only when a new job was created.
func (m *Manager) CreateUnique(appID, activePrefix string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if strings.HasPrefix(job.AppID, activePrefix) && !job.Snapshot().Done {
			return job, false
		}
	}
	return m.createLocked(appID), true
}

func (m *Manager) createLocked(appID string) *Job {
	m.seq++
	now := time.Now()
	j := &Job{
		ID:    fmt.Sprintf("job-%d", m.seq),
		AppID: appID,
		seq:   m.seq,
		subs:  make(map[chan Update]struct{}),
		start: now,
		state: Update{Phase: "pending", Message: "Queued", StartedAt: now.UTC().Format(time.RFC3339), UpdatedAt: now.UTC().Format(time.RFC3339)},
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
	m.pruneTerminalLocked()
	out := make([]Snapshot, 0, len(m.jobs))
	for _, j := range m.jobs {
		if prefix != "" && !strings.HasPrefix(j.AppID, prefix) {
			continue
		}
		out = append(out, Snapshot{ID: j.ID, AppID: j.AppID, Update: j.Snapshot()})
	}
	sort.Slice(out, func(i, k int) bool {
		return m.jobs[out[i].ID].seq < m.jobs[out[k].ID].seq
	})
	return out
}

func (m *Manager) pruneTerminalLocked() {
	limit := m.maxTerminal
	if limit <= 0 {
		limit = 200
	}
	terminal := make([]*Job, 0)
	for _, job := range m.jobs {
		if job.Snapshot().Done {
			terminal = append(terminal, job)
		}
	}
	if len(terminal) <= limit {
		return
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].seq > terminal[j].seq })
	for _, job := range terminal[limit:] {
		delete(m.jobs, job.ID)
	}
}

// Snapshot returns the current state.
func (j *Job) Snapshot() Update {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshotLocked(time.Now())
}

// Subscribe returns a buffered channel pre-loaded with the current state.
func (j *Job) Subscribe() chan Update {
	j.mu.Lock()
	defer j.mu.Unlock()
	ch := make(chan Update, 16)
	ch <- j.snapshotLocked(time.Now())
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
	fn(&j.state)
	now := time.Now()
	j.state.UpdatedAt = now.UTC().Format(time.RFC3339)
	update := j.snapshotLocked(now)
	for ch := range j.subs {
		select {
		case ch <- update:
		default: // slow subscriber: drop intermediate update, it'll get the next one
		}
	}
	observer := j.observer
	j.mu.Unlock()
	if observer != nil {
		observer(update)
	}
}

// Observe installs a non-blocking-caller progress sink. The sink runs after
// the Job lock is released, so a durable journal can persist the update without
// deadlocking Snapshot or another progress call.
func (j *Job) Observe(observer func(Update)) {
	j.mu.Lock()
	j.observer = observer
	update := j.snapshotLocked(time.Now())
	j.mu.Unlock()
	if observer != nil {
		observer(update)
	}
}

func (j *Job) snapshotLocked(now time.Time) Update {
	u := j.state
	u.Nodes = append([]NodeProgress(nil), j.state.Nodes...)
	if !j.start.IsZero() {
		u.ElapsedSecs = int64(now.Sub(j.start).Seconds())
	}
	if !u.Done && u.etaOverride > 0 {
		u.ETASecs = u.etaOverride
	} else if !u.Done && u.Percent > 0 && u.Percent < 100 && u.ElapsedSecs > 0 {
		u.ETASecs = u.ElapsedSecs * int64(100-u.Percent) / int64(u.Percent)
	} else {
		u.ETASecs = 0
	}
	return u
}

// ProgressNodes reports exact per-node preparation state for distributed
// operations while retaining aggregate byte progress for the main progress bar.
func (j *Job) ProgressNodes(phase, message string, nodes []NodeProgress, done, total int64) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.etaOverride = 0
			u.ItemsDone, u.ItemsTotal, u.CurrentItem = 0, 0, ""
		}
		u.Phase = phase
		u.Message = message
		u.Nodes = append([]NodeProgress(nil), nodes...)
		u.BytesDone = done
		u.BytesTotal = total
		if total > 0 {
			u.Percent = clampPercent(int(done * 100 / total))
		}
	})
}

// Progress reports pull/start progress. Pass done/total < 0 to leave them unchanged.
func (j *Job) Progress(phase, msg string, done, total int) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.BytesDone = 0
			u.BytesTotal = 0
			u.Nodes = nil
			u.etaOverride = 0
			u.ItemsDone, u.ItemsTotal, u.CurrentItem = 0, 0, ""
		}
		u.Phase = phase
		u.Message = msg
		if done >= 0 {
			u.LayersDone = done
		}
		if total >= 0 {
			u.LayersTotal = total
		}
		if done >= 0 && total > 0 {
			u.Percent = clampPercent(done * 100 / total)
		}
	})
}

// ProgressOperation reports overall progress for a multi-stage operation while
// retaining the lower-level layer/byte fields for detailed UI presentation.
func (j *Job) ProgressOperation(phase, msg, currentItem string, percent, done, total int) {
	j.progressOperation(phase, msg, currentItem, percent, done, total, 0)
}

// ProgressOperationETA reports overall multi-stage progress with a phase-local
// ETA supplied by the underlying operation (for example, vLLM checkpoint
// loading). The explicit estimate is preferable to extrapolating from the
// lifetime of the entire parent job.
func (j *Job) ProgressOperationETA(phase, msg, currentItem string, percent, done, total int, etaSeconds int64) {
	j.progressOperation(phase, msg, currentItem, percent, done, total, etaSeconds)
}

func (j *Job) progressOperation(phase, msg, currentItem string, percent, done, total int, etaSeconds int64) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.BytesDone = 0
			u.BytesTotal = 0
			u.Nodes = nil
			u.LayersDone = 0
			u.LayersTotal = 0
		}
		u.Phase = phase
		u.Message = msg
		u.CurrentItem = currentItem
		u.Percent = clampPercent(percent)
		u.ItemsDone = done
		u.ItemsTotal = total
		u.etaOverride = max(0, etaSeconds)
	})
}

// ProgressDetail updates phase-local counters without replacing the operation's
// overall percentage. App image pulls use it for layer detail and ETA together.
func (j *Job) ProgressDetail(phase, msg string, done, total int) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.etaOverride = 0
			u.ItemsDone, u.ItemsTotal, u.CurrentItem = 0, 0, ""
		}
		u.Phase = phase
		u.Message = msg
		u.LayersDone = done
		u.LayersTotal = total
	})
}

// ProgressBytes reports byte-level progress for downloads.
func (j *Job) ProgressBytes(phase, msg string, done, total int64) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.Nodes = nil
			u.etaOverride = 0
			u.ItemsDone, u.ItemsTotal, u.CurrentItem = 0, 0, ""
		}
		u.Phase = phase
		u.Message = msg
		u.BytesDone = done
		u.BytesTotal = total
		if total > 0 {
			u.Percent = clampPercent(int(done * 100 / total))
		}
	})
}

// ProgressBytesDetail adds byte counters to an operation that already owns an
// overall multi-stage percentage. It deliberately preserves that percentage.
func (j *Job) ProgressBytesDetail(phase, msg string, done, total int64) {
	j.apply(func(u *Update) {
		if u.Phase != phase {
			u.etaOverride = 0
			u.ItemsDone, u.ItemsTotal, u.CurrentItem = 0, 0, ""
		}
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
		u.Percent = 100
		u.ETASecs = 0
		u.etaOverride = 0
		u.Done = true
	})
}

// Fail marks the job as errored (terminal failure).
func (j *Job) Fail(err error) {
	j.apply(func(u *Update) {
		u.Phase = "error"
		u.Message = "Failed"
		u.Error = err.Error()
		u.ETASecs = 0
		u.etaOverride = 0
		u.Done = true
	})
}

// Cancel marks the job as deliberately stopped by the user.
func (j *Job) Cancel() {
	j.apply(func(u *Update) {
		u.Phase = "canceled"
		u.Message = "Canceled"
		u.Error = ""
		u.ETASecs = 0
		u.etaOverride = 0
		u.Done = true
	})
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
