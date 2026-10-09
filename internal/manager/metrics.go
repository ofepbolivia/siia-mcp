package manager

import "sync"

// metrics holds bounded in-memory counters (SDD §18.3). Labels are limited to
// fixed buckets: tool, outcome, and a handful of operation names; never
// connections, objects, SQL, or free-form values.
type metrics struct {
	mu       sync.Mutex
	counters map[string]int64
	active   int64
}

func newMetrics() *metrics {
	return &metrics{counters: map[string]int64{}}
}

// Inc bumps a bounded counter by one.
func (m *metrics) Inc(name string) {
	m.mu.Lock()
	m.counters[name]++
	m.mu.Unlock()
}

// Enter/Leave track the number of requests currently being processed.
func (m *metrics) Enter() { m.mu.Lock(); m.active++; m.mu.Unlock() }

func (m *metrics) Leave() { m.mu.Lock(); m.active--; m.mu.Unlock() }

// Snapshot returns a defensive copy of the counters.
func (m *metrics) Snapshot() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.counters))
	for k, v := range m.counters {
		out[k] = v
	}
	out["active"] = m.active
	return out
}
