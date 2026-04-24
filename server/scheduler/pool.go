package scheduler

import (
	"sort"
	"sync"
	"time"
)

// DevicePool is the in-memory snapshot of every device's most-recently-known
// network stats. The heartbeat sink calls Update once per heartbeat; the
// scheduler calls Snapshot at pick time. It is intentionally lock-light so
// Snapshot stays sub-millisecond even with hundreds of devices.
type DevicePool struct {
	now func() time.Time

	mu sync.RWMutex
	m  map[string]deviceStats
}

// DeviceUpdate is what the heartbeat sink hands in. Nil pointers mean
// "no fresh metric in this heartbeat" and are not applied (avoids clobbering
// the previous value with a heartbeat that hasn't yet collected 5 samples).
type DeviceUpdate struct {
	Country  string
	NetType  string
	AvgRTTms *int64
	LossRate *float64
	SeenAt   time.Time
}

// Candidate is what the selector sees. It is a value-type snapshot so the
// selector can sort/filter without holding the pool mutex.
type Candidate struct {
	DeviceID string
	Country  string
	NetType  string
	AvgRTT   int64   // ms; 0 when HasStats=false
	LossRate float64 // 0..1
	HasStats bool    // false until at least one heartbeat carried metrics
	LastSeen time.Time
}

type deviceStats struct {
	country  string
	netType  string
	avgRTT   int64
	lossRate float64
	hasStats bool
	lastSeen time.Time
}

// NewPool creates an empty pool. Pass nil for nowFn to default to time.Now.
func NewPool(nowFn func() time.Time) *DevicePool {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &DevicePool{
		now: nowFn,
		m:   make(map[string]deviceStats),
	}
}

// Update merges u into the entry for deviceID. SeenAt is always advanced
// (every heartbeat is a freshness signal even when it lacks metrics).
func (p *DevicePool) Update(deviceID string, u DeviceUpdate) {
	if deviceID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.m[deviceID]
	if u.Country != "" {
		cur.country = u.Country
	}
	if u.NetType != "" {
		cur.netType = u.NetType
	}
	if u.AvgRTTms != nil {
		cur.avgRTT = *u.AvgRTTms
		cur.hasStats = true
	}
	if u.LossRate != nil {
		cur.lossRate = *u.LossRate
		cur.hasStats = true
	}
	if !u.SeenAt.IsZero() {
		cur.lastSeen = u.SeenAt
	} else {
		cur.lastSeen = p.now()
	}
	p.m[deviceID] = cur
}

// Forget drops the entry for deviceID. Safe to call for unknown ids.
// Optional: the selector already filters by the lister's online set, so
// stale entries are harmless; calling Forget on disconnect is just hygiene.
func (p *DevicePool) Forget(deviceID string) {
	p.mu.Lock()
	delete(p.m, deviceID)
	p.mu.Unlock()
}

// Snapshot returns one Candidate per device that is in `online`. Callers may
// pass nil online to get every device the pool knows about (useful for tests
// and admin-style introspection).
//
// Devices that the pool has never heard about (no heartbeat yet) but that are
// listed in `online` are still returned, as Candidates with HasStats=false.
// This avoids starving a freshly-registered device until its first metrics-
// carrying heartbeat.
func (p *DevicePool) Snapshot(online map[string]struct{}) []Candidate {
	p.mu.RLock()
	defer p.mu.RUnlock()

	include := func(id string) bool {
		if online == nil {
			return true
		}
		_, ok := online[id]
		return ok
	}

	out := make([]Candidate, 0, len(p.m)+len(online))
	seen := make(map[string]struct{}, len(p.m))
	for id, s := range p.m {
		if !include(id) {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, Candidate{
			DeviceID: id,
			Country:  s.country,
			NetType:  s.netType,
			AvgRTT:   s.avgRTT,
			LossRate: s.lossRate,
			HasStats: s.hasStats,
			LastSeen: s.lastSeen,
		})
	}
	for id := range online {
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, Candidate{DeviceID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

// Size is the number of devices the pool knows about. Mostly for tests/metrics.
func (p *DevicePool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.m)
}
