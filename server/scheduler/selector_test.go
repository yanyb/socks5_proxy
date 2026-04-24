package scheduler

import (
	"errors"
	"testing"
	"time"
)

func mkCand(id, country string, rttMs int64, loss float64, lastSeen time.Time) Candidate {
	return Candidate{
		DeviceID: id, Country: country,
		AvgRTT: rttMs, LossRate: loss,
		HasStats: true, LastSeen: lastSeen,
	}
}

func TestScored_PicksLowestScore(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	tok, _ := ParseUsername("B_1_US_5_Ab000001")
	in := SelectInput{
		Token: tok,
		Candidates: []Candidate{
			mkCand("dev-a", "US", 200, 0.0, now), // score 200
			mkCand("dev-b", "US", 50, 0.05, now), // score 100  <-- pick
			mkCand("dev-c", "US", 80, 0.0, now),  // score 80   <-- actually pick
		},
		Now: now,
	}
	got, err := sel.Pick(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "dev-c" {
		t.Fatalf("got %q, want dev-c", got)
	}
}

func TestScored_FilterCountry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	tok, _ := ParseUsername("B_1_DE_5_Ab000001")
	in := SelectInput{
		Token: tok,
		Candidates: []Candidate{
			mkCand("dev-a", "US", 10, 0, now),
			mkCand("dev-b", "DE", 100, 0, now),
		},
		Now: now,
	}
	got, err := sel.Pick(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "dev-b" {
		t.Fatalf("got %q, want dev-b", got)
	}
}

func TestScored_NoMatchInCountry_Errors(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	tok, _ := ParseUsername("B_1_DE_5_Ab000001")
	_, err := sel.Pick(SelectInput{
		Token:      tok,
		Candidates: []Candidate{mkCand("dev-a", "US", 10, 0, now)},
		Now:        now,
	})
	if !errors.Is(err, ErrNoDevice) {
		t.Fatalf("want ErrNoDevice, got %v", err)
	}
}

func TestScored_ExcludeLeasedByOthers(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	tok, _ := ParseUsername("B_1_US_5_Ab000001")
	in := SelectInput{
		Token: tok,
		Candidates: []Candidate{
			mkCand("dev-a", "US", 10, 0, now),
			mkCand("dev-b", "US", 100, 0, now),
		},
		LeasedByOthers: map[string]struct{}{"dev-a": {}},
		Now:            now,
	}
	got, _ := sel.Pick(in)
	if got != "dev-b" {
		t.Fatalf("dev-a is taken; got %q, want dev-b", got)
	}
}

func TestScored_StaleDropped(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	sel.StaleAfter = 30 * time.Second
	tok, _ := ParseUsername("B_1_US_5_Ab000001")
	in := SelectInput{
		Token: tok,
		Candidates: []Candidate{
			mkCand("dev-stale", "US", 5, 0, now.Add(-2*time.Minute)),
			mkCand("dev-fresh", "US", 100, 0, now.Add(-1*time.Second)),
		},
		Now: now,
	}
	got, err := sel.Pick(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "dev-fresh" {
		t.Fatalf("stale should be dropped; got %q", got)
	}
}

func TestScored_NoStatsUsesDefault(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sel := NewScoredSelector()
	tok, _ := ParseUsername("B_1_US_5_Ab000001")
	in := SelectInput{
		Token: tok,
		Candidates: []Candidate{
			{DeviceID: "dev-new", Country: "US", LastSeen: now},     // HasStats=false; effective rtt = 200
			mkCand("dev-known", "US", 250, 0.0, now),                // score 250 -> dev-new wins
		},
		Now: now,
	}
	got, _ := sel.Pick(in)
	if got != "dev-new" {
		t.Fatalf("expected dev-new (default rtt 200 < 250); got %q", got)
	}
}
