package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// SelectInput is everything a Selector needs. The scheduler hands these in;
// the Selector returns one device_id (or an error). Pure function -- no I/O,
// no goroutines -- so swapping algorithms (e.g. a weighted round-robin or an
// admin-driven score) is a one-file change.
type SelectInput struct {
	Token          UserToken
	Candidates     []Candidate         // already filtered to "online"
	LeasedByOthers map[string]struct{} // device_ids occupied by other user_keys
	Now            time.Time
}

// Selector decides which device serves this user. Implementations MUST be
// safe to call concurrently and should not mutate SelectInput.
type Selector interface {
	Pick(in SelectInput) (deviceID string, err error)
}

// ErrNoDevice is returned when no candidate satisfies the request.
var ErrNoDevice = errors.New("scheduler: no device available")

// ScoredSelector is the default Selector:
//
//  1. If Token.Country != "", filter candidates to that country.
//  2. Drop candidates whose LastSeen is older than StaleAfter.
//  3. Drop candidates that LeasedByOthers contains.
//  4. Score each surviving candidate:
//     score = effective_rtt + LossPenaltyMs * loss_rate
//     where effective_rtt = AvgRTT (or DefaultRTTms when HasStats=false).
//  5. Pick the lowest score; tie-break by device_id (stable across restarts).
//
// This is intentionally simple. Two knobs cover most ops needs:
//   - LossPenaltyMs: how much extra "ms" each unit of loss costs.
//     1000 means 10% loss costs an extra 100ms in scoring.
//   - DefaultRTTms: the assumed RTT for a freshly-registered device that
//     hasn't yet sent enough heartbeats to publish metrics. Keep moderate so
//     new devices aren't permanently starved.
type ScoredSelector struct {
	LossPenaltyMs float64
	DefaultRTTms  int64
	StaleAfter    time.Duration
}

// NewScoredSelector returns a ScoredSelector with sensible defaults.
func NewScoredSelector() *ScoredSelector {
	return &ScoredSelector{
		LossPenaltyMs: 1000, // 10% loss == +100ms
		DefaultRTTms:  200,
		StaleAfter:    90 * time.Second,
	}
}

// Pick implements Selector. See type docs for algorithm.
func (s *ScoredSelector) Pick(in SelectInput) (string, error) {
	wantCountry := in.Token.Country
	type scored struct {
		id    string
		score float64
	}
	keep := make([]scored, 0, len(in.Candidates))

	for _, c := range in.Candidates {
		if wantCountry != "" && c.Country != wantCountry {
			continue
		}
		if s.StaleAfter > 0 && !c.LastSeen.IsZero() && in.Now.Sub(c.LastSeen) > s.StaleAfter {
			continue
		}
		if _, taken := in.LeasedByOthers[c.DeviceID]; taken {
			continue
		}
		rtt := float64(c.AvgRTT)
		if !c.HasStats {
			rtt = float64(s.DefaultRTTms)
		}
		keep = append(keep, scored{
			id:    c.DeviceID,
			score: rtt + s.LossPenaltyMs*c.LossRate,
		})
	}
	if len(keep) == 0 {
		if wantCountry != "" {
			return "", fmt.Errorf("%w (country=%s)", ErrNoDevice, wantCountry)
		}
		return "", ErrNoDevice
	}
	sort.Slice(keep, func(i, j int) bool {
		if keep[i].score != keep[j].score {
			return keep[i].score < keep[j].score
		}
		return keep[i].id < keep[j].id
	})
	return keep[0].id, nil
}
