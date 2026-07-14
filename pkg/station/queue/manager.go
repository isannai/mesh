package queue

import (
	"context"
	"sync"

	"github.com/isannai/mesh/pkg/setup"
)

// Factory builds a Config and ProcessFunc for a given service. Phase 3
// supplies the production implementation (resolveMaxQueue, resolveConcurrency,
// dispatcher.MakeProcess); Phase 2 callers and tests inject stubs.
//
// Returning a nil ProcessFunc is allowed — the Manager will skip worker
// startup, leaving the Queue dormant. Useful for tests that exercise Submit/
// Stats without actually dispatching.
type Factory func(svc setup.ServiceEntry) (Config, ProcessFunc)

// Manager owns one Queue per service name, lazily created on first access.
//
// Each Queue gets its own Worker goroutine bound to the Manager's context,
// so canceling that context drains all workers in tandem.
type Manager struct {
	ctx     context.Context
	factory Factory

	mu     sync.RWMutex
	queues map[string]*Queue
}

// NewManager builds an empty Manager. ctx governs the lifetime of every
// Queue's Worker — cancel it to shut down all queues.
func NewManager(ctx context.Context, factory Factory) *Manager {
	if factory == nil {
		// safe default — empty config + nil process; tests pass this when
		// they only need the map machinery.
		factory = func(svc setup.ServiceEntry) (Config, ProcessFunc) {
			return Config{ServiceName: svc.Name}, nil
		}
	}
	return &Manager{
		ctx:     ctx,
		factory: factory,
		queues:  make(map[string]*Queue),
	}
}

// GetOrCreate returns the queue owned by svc, creating it (and starting its
// Worker) on first access. Safe for concurrent calls.
//
// The returned Queue is the canonical instance for svc.Name — repeated calls
// with the same name yield the same pointer regardless of svc field changes.
// This means Phase 1 cannot dynamically resize an existing queue's MaxQueue;
// that's the job of L3 runtime push (out of scope for this phase).
func (m *Manager) GetOrCreate(svc setup.ServiceEntry) *Queue {
	m.mu.RLock()
	if q, ok := m.queues[svc.Name]; ok {
		m.mu.RUnlock()
		return q
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check after acquiring write lock — another goroutine may have
	// raced ahead and created the queue while we were upgrading.
	if q, ok := m.queues[svc.Name]; ok {
		return q
	}

	cfg, process := m.factory(svc)
	if cfg.ServiceName == "" {
		cfg.ServiceName = svc.Name
	}
	q := New(cfg)
	if process != nil {
		go q.Worker(m.ctx, process)
	}
	m.queues[svc.Name] = q
	return q
}

// Get returns the queue cached for name, or nil if no queue has been created
// yet. Use this when you do not want to trigger lazy creation (e.g. read-only
// stats endpoints).
func (m *Manager) Get(name string) *Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queues[name]
}

// Stats returns aggregate metrics for the named service. Returns a zero-value
// Stats when no queue exists for that name.
func (m *Manager) Stats(name string) Stats {
	if q := m.Get(name); q != nil {
		return q.Stats()
	}
	return Stats{}
}

// AllStats returns a snapshot of every active queue's stats. Used by the
// heartbeat loop to publish per-service metrics in a single pass without
// holding the manager lock during the publish path.
func (m *Manager) AllStats() map[string]Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Stats, len(m.queues))
	for name, q := range m.queues {
		out[name] = q.Stats()
	}
	return out
}

// Names returns the service names of every active queue, in unspecified
// order. The caller can use these as keys to fetch detail via Stats / Get.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.queues))
	for name := range m.queues {
		out = append(out, name)
	}
	return out
}

// Len returns the number of active queues. Useful for diagnostics.
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.queues)
}
