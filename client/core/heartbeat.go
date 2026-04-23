package core

import (
	"sync"
	"sync/atomic"
)

// HeartbeatStats keeps a 5-sample sliding window of measured RTTs (ms) and a
// rolling loss counter (out of every windowSize heartbeats). It is safe for
// concurrent use: the reader is the heartbeat sender, and the writer can be
// called when an ack arrives or a heartbeat is dropped/timed out.
//
// Semantics agreed with the server:
//   - cur_ts is the client wall clock at send time (ms). The server echoes it
//     back unchanged in heartbeat_ack; RTT = now-ms - cur_ts.
//   - avg_rtt and loss_rate are emitted only after at least windowSize samples
//     have accumulated. Until then the heartbeat omits both fields.
type HeartbeatStats struct {
	mu     sync.Mutex
	rtts   []int64 // ms; newest at end, capped at window
	sent   int     // number of heartbeats sent in the current loss window
	lost   int     // number of those heartbeats with no/late/error ack
	window int

	// netType is read on every heartbeat send; updated rarely from outside.
	netType atomic.Value // string
}

// NewHeartbeatStats returns a stats tracker with the given sliding window size.
// window <= 0 falls back to the spec default of 5.
func NewHeartbeatStats(window int) *HeartbeatStats {
	if window <= 0 {
		window = 5
	}
	return &HeartbeatStats{window: window, rtts: make([]int64, 0, window)}
}

// SetNetType updates the network type tag (e.g. "wifi", "5g", "4g"). Safe to
// call concurrently with the heartbeat loop.
func (s *HeartbeatStats) SetNetType(t string) { s.netType.Store(t) }

// NetType returns the most recently set network type, or "" if unset.
func (s *HeartbeatStats) NetType() string {
	if v, _ := s.netType.Load().(string); v != "" {
		return v
	}
	return ""
}

// Sent must be called once per heartbeat the loop attempts to write.
func (s *HeartbeatStats) Sent() {
	s.mu.Lock()
	s.sent++
	s.mu.Unlock()
}

// AckOK records a measured RTT in milliseconds for the latest heartbeat.
func (s *HeartbeatStats) AckOK(rttMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rttMs < 0 {
		rttMs = 0
	}
	if len(s.rtts) >= s.window {
		s.rtts = append(s.rtts[:0], s.rtts[1:]...)
	}
	s.rtts = append(s.rtts, rttMs)
}

// AckLost records that a heartbeat did not produce a usable ack.
func (s *HeartbeatStats) AckLost() {
	s.mu.Lock()
	s.lost++
	s.mu.Unlock()
}

// SnapshotForSend reads the current window: it returns the avg RTT (ms) and
// loss rate (0..1) iff the window has windowSize completed samples; otherwise
// the *int64 / *float64 are nil and should be omitted from the wire.
//
// As a side effect, when the loss window completes (sent >= windowSize) the
// lost/sent counters reset for the next window.
func (s *HeartbeatStats) SnapshotForSend() (avgRTTms *int64, lossRate *float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rtts) >= s.window {
		var sum int64
		for _, v := range s.rtts {
			sum += v
		}
		avg := sum / int64(len(s.rtts))
		avgRTTms = &avg
	}
	if s.sent >= s.window {
		lr := float64(s.lost) / float64(s.sent)
		if lr < 0 {
			lr = 0
		}
		if lr > 1 {
			lr = 1
		}
		lossRate = &lr
		s.sent = 0
		s.lost = 0
	}
	return
}
