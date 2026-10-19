package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// avgWindow is how many of the latest finished jobs the average job time is
// taken over. Ten, not more: a node's speed shows up in the jobs it ran
// lately, and an engine that was swapped an hour ago should not still be
// pulling the average toward the old one.
const avgWindow = 10

// Counters is what one queue remembers across a restart: how many jobs it has
// answered, and the times of the last few, so the average does not start from
// nothing every time the station comes back.
type Counters struct {
	// Engine is the engine behind the service when these were counted. Counts
	// earned by one engine are dropped when another takes the service name.
	Engine string `json:"engine,omitempty"`
	// Done is the number of jobs the engine answered. Failed jobs are not in it.
	Done   int64 `json:"done"`
	Failed int64 `json:"failed,omitempty"`
	// RecentMs holds the time each of the last avgWindow jobs took, oldest
	// first, from the moment the job left the queue to the moment it finished.
	RecentMs  []int64 `json:"recent_ms,omitempty"`
	UpdatedAt int64   `json:"updated_at,omitempty"` // unix ms
}

type counterFile struct {
	Version  int                 `json:"version"`
	Services map[string]Counters `json:"services"`
}

// CounterStore keeps every service's Counters in one small JSON file. It is
// written after each finished job (a few hundred bytes), through a temporary
// file and a rename so a crash in the middle never leaves half a file.
//
// A nil *CounterStore is valid and does nothing, so a queue without one simply
// counts in memory as before.
type CounterStore struct {
	path string

	mu     sync.Mutex
	m      map[string]Counters
	warned bool // a failed write is logged once, not on every job
}

// OpenCounterStore reads the file at path. A missing file is an empty store. A
// file that cannot be read as JSON is moved aside to <path>.bad and the store
// starts empty; the error says so, and the store is still usable.
func OpenCounterStore(path string) (*CounterStore, error) {
	s := &CounterStore{path: path, m: map[string]Counters{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	var f counterFile
	if err := json.Unmarshal(b, &f); err != nil {
		_ = os.Rename(path, path+".bad")
		return s, fmt.Errorf("%s is not valid JSON, moved to %s.bad: %w", path, path, err)
	}
	for k, v := range f.Services {
		s.m[k] = v
	}
	return s, nil
}

// Get returns what is stored for service, or the zero value.
func (s *CounterStore) Get(service string) Counters {
	if s == nil {
		return Counters{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.m[service]
	c.RecentMs = append([]int64(nil), c.RecentMs...)
	return c
}

// Put stores c for service and writes the file.
//
// Two jobs of one service can finish together and each take its snapshot
// before either writes. The one with the smaller total must not overwrite the
// bigger one, so a write that would take the count backwards is dropped.
func (s *CounterStore) Put(service string, c Counters) {
	if s == nil || service == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.m[service]; ok && old.Engine == c.Engine && old.Done+old.Failed > c.Done+c.Failed {
		return
	}
	s.m[service] = c
	if err := s.writeLocked(); err != nil && !s.warned {
		s.warned = true
		log.Printf("[queue] job counts are not saved: %v", err)
	}
}

func (s *CounterStore) writeLocked() error {
	b, err := json.MarshalIndent(counterFile{Version: 1, Services: s.m}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// countersLocked snapshots what is worth remembering. The caller holds q.mu.
func (q *Queue) countersLocked() Counters {
	c := Counters{
		Engine:    q.cfg.Engine,
		Done:      q.totalDone,
		Failed:    q.totalFailed,
		UpdatedAt: time.Now().UnixMilli(),
	}
	for _, d := range q.durations {
		c.RecentMs = append(c.RecentMs, d.Milliseconds())
	}
	return c
}

// restore loads the saved counters into a fresh queue. Counts saved for another
// engine are not taken over.
func (q *Queue) restore() {
	if q.cfg.Counts == nil {
		return
	}
	c := q.cfg.Counts.Get(q.cfg.ServiceName)
	if c.Engine != "" && q.cfg.Engine != "" && c.Engine != q.cfg.Engine {
		return
	}
	q.totalDone, q.totalFailed = c.Done, c.Failed
	recent := c.RecentMs
	if len(recent) > avgWindow {
		recent = recent[len(recent)-avgWindow:]
	}
	for _, ms := range recent {
		q.durations = append(q.durations, time.Duration(ms)*time.Millisecond)
	}
}
