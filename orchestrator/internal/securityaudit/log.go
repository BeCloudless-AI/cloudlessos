// Package securityaudit provides the single bounded, secret-free audit trail
// for security-relevant Cloudless actions.
package securityaudit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	MaxBytes  = 2 << 20
	MaxEvents = 200
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+\S+`),
	regexp.MustCompile(`\bsk-cloudless-[A-Za-z0-9._-]+`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]+`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]+`),
	regexp.MustCompile(`(?i)\b(token|secret|password|authorization)\s*[:=]\s*\S+`),
}

type Event struct {
	Time        string `json:"time"`
	Category    string `json:"category,omitempty"`
	Event       string `json:"event"`
	Outcome     string `json:"outcome"`
	Actor       string `json:"actor,omitempty"`
	Target      string `json:"target,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	KeyID       string `json:"keyId,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	Source      string `json:"source,omitempty"`
	Status      int    `json:"status,omitempty"`
	DurationMS  int64  `json:"durationMs,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type Log struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func New(dir string) *Log {
	return &Log{path: filepath.Join(dir, "security-audit.jsonl"), now: time.Now}
}

func redact(value string) string {
	value = strings.TrimSpace(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "<redacted>")
	}
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func clean(event Event) Event {
	event.Category, event.Event, event.Outcome = redact(event.Category), redact(event.Event), redact(event.Outcome)
	event.Actor, event.Target, event.OperationID = redact(event.Actor), redact(event.Target), redact(event.OperationID)
	event.KeyID, event.Scope, event.Method = redact(event.KeyID), redact(event.Scope), redact(event.Method)
	event.Path, event.Source, event.Detail = redact(event.Path), redact(event.Source), redact(event.Detail)
	return event
}

func (l *Log) Append(event Event) {
	if l == nil || l.path == "" || strings.TrimSpace(event.Event) == "" || strings.TrimSpace(event.Outcome) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	event = clean(event)
	event.Time = l.now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	if info, err := os.Stat(l.path); err == nil && info.Size()+int64(len(data)+1) > MaxBytes {
		_ = os.Remove(l.path + ".1")
		_ = os.Rename(l.path, l.path+".1")
	}
	if os.MkdirAll(filepath.Dir(l.path), 0o700) != nil {
		return
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
	_ = file.Close()
}

func (l *Log) Latest(limit int) ([]Event, error) {
	if l == nil || l.path == "" {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if limit <= 0 || limit > MaxEvents {
		limit = 100
	}
	events := make([]Event, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if len(events) == limit {
			copy(events, events[1:])
			events = events[:limit-1]
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}
