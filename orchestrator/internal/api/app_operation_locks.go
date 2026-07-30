package api

import (
	"sort"
	"sync"
)

// appOperationLocks serializes operations only when they touch the same catalog
// app. Disjoint app installs/removals remain concurrent. Multi-app packs acquire
// their complete, sorted lock set so overlapping dependencies cannot deadlock.
type appOperationLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (l *appOperationLocks) lock(ids ...string) func() {
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)

	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*sync.Mutex)
	}
	locks := make([]*sync.Mutex, 0, len(ordered))
	for _, id := range ordered {
		if l.locks[id] == nil {
			l.locks[id] = &sync.Mutex{}
		}
		locks = append(locks, l.locks[id])
	}
	l.mu.Unlock()

	for _, lock := range locks {
		lock.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}
}
