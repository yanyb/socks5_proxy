package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DeviceLister is the seam to the device registry. Only "is X online?" and
// "list online" are needed; the registry's yamux internals stay private.
//
// Implementing it on *hub.Registry is a one-method addition; that's the only
// touchpoint between scheduler and hub.
type DeviceLister interface {
	OnlineSet() map[string]struct{}
}

// Scheduler ties username parsing, the device pool, sticky leases and a
// Selector together. Concurrency: methods are safe to call from any number
// of goroutines; serialization is delegated to the underlying components.
type Scheduler struct {
	Lister   DeviceLister
	Pool     *DevicePool
	Leases   *StickyLeases
	Selector Selector
	Log      *logrus.Logger

	now func() time.Time

	stopOnce sync.Once
	stop     chan struct{}
}

// Config groups optional knobs. Most users only set Selector (or use the
// default) and the sweep interval.
type Config struct {
	Lister   DeviceLister
	Pool     *DevicePool
	Leases   *StickyLeases
	Selector Selector
	Log      *logrus.Logger

	// SweepInterval triggers periodic StickyLeases.Sweep(). 0 = 1 minute.
	SweepInterval time.Duration

	// Now overrides time.Now for tests. nil = time.Now.
	Now func() time.Time
}

// New builds a Scheduler. Required: Lister, Pool, Leases. If Selector is nil,
// a NewScoredSelector() is used. If Log is nil, a quiet logger is used.
func New(cfg Config) (*Scheduler, error) {
	if cfg.Lister == nil {
		return nil, errors.New("scheduler: Lister is required")
	}
	if cfg.Pool == nil {
		return nil, errors.New("scheduler: Pool is required")
	}
	if cfg.Leases == nil {
		return nil, errors.New("scheduler: Leases is required")
	}
	if cfg.Selector == nil {
		cfg.Selector = NewScoredSelector()
	}
	if cfg.Log == nil {
		cfg.Log = logrus.New()
		cfg.Log.SetLevel(logrus.WarnLevel)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Scheduler{
		Lister:   cfg.Lister,
		Pool:     cfg.Pool,
		Leases:   cfg.Leases,
		Selector: cfg.Selector,
		Log:      cfg.Log,
		now:      cfg.Now,
		stop:     make(chan struct{}),
	}, nil
}

// StartSweeper runs StickyLeases.Sweep on a fixed cadence. Returns
// immediately; cancel via Stop() or by cancelling ctx.
func (s *Scheduler) StartSweeper(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-t.C:
				if n := s.Leases.Sweep(); n > 0 {
					s.Log.WithField("evicted", n).Debug("scheduler: lease sweep")
				}
			}
		}
	}()
}

// Stop terminates the background sweeper. Idempotent.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Pick is the hot path called by the SOCKS5 dial callback.
//
// Returns (device_id, handle, error). On success the caller MUST eventually
// Close() the handle (typically by wrapping the resulting net.Conn with
// WrapConn). On error there is nothing to clean up.
//
// Algorithm:
//  1. Parse username (auth happens elsewhere; we only need scheduling fields).
//  2. If user has an active lease and that device is still online -> reuse.
//  3. Otherwise pick a fresh device via Selector and acquire a new lease.
func (s *Scheduler) Pick(ctx context.Context, username string) (string, Handle, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	tok, err := ParseUsername(username)
	if err != nil {
		return "", nil, err
	}

	online := s.Lister.OnlineSet()
	if len(online) == 0 {
		return "", nil, fmt.Errorf("%w: no devices online", ErrNoDevice)
	}

	userKey := tok.UserKey()

	// Sticky reuse path.
	if l := s.Leases.GetActive(userKey); l != nil {
		if _, ok := online[l.DeviceID]; ok {
			return l.DeviceID, s.Leases.AcquireRef(l), nil
		}
		// Bound device went offline mid-lease; force a fresh pick.
		s.Log.WithFields(logrus.Fields{
			"user_key":  userKey,
			"device_id": l.DeviceID,
		}).Info("scheduler: bound device offline, repicking")
	}

	candidates := s.Pool.Snapshot(online)
	leasedByOthers := s.Leases.HeldDeviceIDs(userKey)

	devID, err := s.Selector.Pick(SelectInput{
		Token:          tok,
		Candidates:     candidates,
		LeasedByOthers: leasedByOthers,
		Now:            s.now(),
	})
	if err != nil {
		return "", nil, err
	}

	_, h, err := s.Leases.Acquire(userKey, devID, tok.Country, tok.Duration)
	if err != nil {
		// Race with another goroutine grabbing the same device. Retry once
		// against the now-updated leasedByOthers set; if it fails again we
		// give up and surface the error.
		leasedByOthers = s.Leases.HeldDeviceIDs(userKey)
		devID, err2 := s.Selector.Pick(SelectInput{
			Token:          tok,
			Candidates:     candidates,
			LeasedByOthers: leasedByOthers,
			Now:            s.now(),
		})
		if err2 != nil {
			return "", nil, err2
		}
		_, h, err = s.Leases.Acquire(userKey, devID, tok.Country, tok.Duration)
		if err != nil {
			return "", nil, err
		}
		return devID, h, nil
	}
	return devID, h, nil
}

// WrapConn returns a net.Conn that calls handle.Close() in addition to closing
// c. Use it to keep the lease refcount accurate for the lifetime of a SOCKS5
// stream.
func WrapConn(c net.Conn, h Handle) net.Conn {
	if h == nil {
		return c
	}
	return &leasedConn{Conn: c, h: h}
}

type leasedConn struct {
	net.Conn
	h        Handle
	closeMu  sync.Mutex
	closeErr error
	closed   bool
}

// Close closes the underlying conn first, then releases the lease ref. The
// lease release error is best-effort; the caller cares about the conn's error.
func (lc *leasedConn) Close() error {
	lc.closeMu.Lock()
	defer lc.closeMu.Unlock()
	if lc.closed {
		return lc.closeErr
	}
	lc.closed = true
	lc.closeErr = lc.Conn.Close()
	_ = lc.h.Close()
	return lc.closeErr
}
