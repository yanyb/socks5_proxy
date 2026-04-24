package scheduler

import (
	"errors"
	"sync"
	"time"
)

// Handle is the per-stream "I'm using this lease" reference. The dial path
// holds one and calls Close() on it when the SOCKS5 stream finishes.
//
// Close() is idempotent. Closing a Handle does NOT free the lease for other
// users -- the lease persists until its TTL expires (that's the IP-stickiness
// guarantee from the username spec). Refcount is for accounting only.
type Handle interface {
	Close() error
}

// Lease is what the scheduler returns from Pick. Fields are read-only after
// creation; mutate only through StickyLeases methods.
type Lease struct {
	UserKey  string
	DeviceID string
	Country  string
	Expires  time.Time

	refcount int // guarded by parent StickyLeases.mu
}

// ErrDeviceTaken means another user_key currently leases the requested device.
var ErrDeviceTaken = errors.New("scheduler: device taken by another user")

// StickyLeases is the central lease table. Two indexes:
//
//	byUser[userKey]   -> lease     (sticky binding)
//	byDev[deviceID]   -> lease     (reverse: who occupies this device?)
//
// Both maps point to the same *Lease (single source of truth). All access
// goes through the mutex; no callers see inconsistent indexes.
//
// TTL is enforced lazily: every public method checks expiry and evicts.
// A background sweeper (run by the scheduler) trims long-idle entries from
// memory but doesn't change the contract.
type StickyLeases struct {
	now func() time.Time

	mu     sync.Mutex
	byUser map[string]*Lease
	byDev  map[string]*Lease
}

// NewLeases builds a fresh table. Pass nil for nowFn to default to time.Now.
func NewLeases(nowFn func() time.Time) *StickyLeases {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &StickyLeases{
		now:    nowFn,
		byUser: make(map[string]*Lease),
		byDev:  make(map[string]*Lease),
	}
}

// GetActive returns the user's current lease if it hasn't expired. If the
// lease has expired, it is evicted from both indexes and nil is returned.
//
// The caller does NOT receive a Handle here; use AcquireRef to bump refcount
// once the caller commits to using it.
func (sl *StickyLeases) GetActive(userKey string) *Lease {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	l := sl.byUser[userKey]
	if l == nil {
		return nil
	}
	if !sl.now().Before(l.Expires) {
		sl.evictLocked(l)
		return nil
	}
	return l
}

// AcquireRef bumps the refcount on an active lease and returns a Handle.
// Panics if the lease has been evicted (caller should re-check via GetActive).
func (sl *StickyLeases) AcquireRef(l *Lease) Handle {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.byUser[l.UserKey] != l {
		// Lease was evicted between the caller's GetActive and now. Treat as
		// a miss; the caller will re-pick. Returning a no-op handle keeps the
		// caller's code path uniform.
		return noopHandle{}
	}
	l.refcount++
	return &leaseHandle{sl: sl, l: l}
}

// Acquire creates a fresh lease for userKey on deviceID with the given TTL.
//
//   - If userKey already has a lease (active or expired), it is replaced.
//   - If deviceID is held by a different user_key whose lease has not
//     expired, returns ErrDeviceTaken.
func (sl *StickyLeases) Acquire(userKey, deviceID, country string, ttl time.Duration) (*Lease, Handle, error) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if existing := sl.byDev[deviceID]; existing != nil {
		if !sl.now().Before(existing.Expires) {
			sl.evictLocked(existing)
		} else if existing.UserKey != userKey {
			return nil, nil, ErrDeviceTaken
		}
	}
	if old := sl.byUser[userKey]; old != nil {
		sl.evictLocked(old)
	}

	l := &Lease{
		UserKey:  userKey,
		DeviceID: deviceID,
		Country:  country,
		Expires:  sl.now().Add(ttl),
		refcount: 1,
	}
	sl.byUser[userKey] = l
	sl.byDev[deviceID] = l
	return l, &leaseHandle{sl: sl, l: l}, nil
}

// HeldDeviceIDs returns the set of device_ids currently held by users *other
// than* exceptUserKey. Used by the selector to filter out occupied devices.
// Expired leases are evicted as a side effect.
func (sl *StickyLeases) HeldDeviceIDs(exceptUserKey string) map[string]struct{} {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	now := sl.now()
	out := make(map[string]struct{}, len(sl.byDev))
	for dev, l := range sl.byDev {
		if !now.Before(l.Expires) {
			sl.evictLocked(l)
			continue
		}
		if l.UserKey == exceptUserKey {
			continue
		}
		out[dev] = struct{}{}
	}
	return out
}

// Sweep evicts any lease past its expiry. Cheap O(N) walk; meant to be called
// from a low-frequency background ticker (the scheduler does this).
func (sl *StickyLeases) Sweep() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	now := sl.now()
	n := 0
	for _, l := range sl.byUser {
		if !now.Before(l.Expires) {
			sl.evictLocked(l)
			n++
		}
	}
	return n
}

// Size returns the number of currently-held leases. For metrics/tests.
func (sl *StickyLeases) Size() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return len(sl.byUser)
}

// evictLocked removes a lease from both indexes. Caller holds sl.mu.
func (sl *StickyLeases) evictLocked(l *Lease) {
	if cur, ok := sl.byUser[l.UserKey]; ok && cur == l {
		delete(sl.byUser, l.UserKey)
	}
	if cur, ok := sl.byDev[l.DeviceID]; ok && cur == l {
		delete(sl.byDev, l.DeviceID)
	}
}

// leaseHandle is the per-stream refcount; Close decrements once and only once.
type leaseHandle struct {
	sl     *StickyLeases
	l      *Lease
	closed bool
	mu     sync.Mutex
}

// Close decrements the refcount. Idempotent.
func (h *leaseHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	h.sl.mu.Lock()
	defer h.sl.mu.Unlock()
	if h.l.refcount > 0 {
		h.l.refcount--
	}
	return nil
}

type noopHandle struct{}

func (noopHandle) Close() error { return nil }
