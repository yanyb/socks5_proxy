package scheduler

import (
	"testing"
	"time"
)

func iptr(v int64) *int64     { return &v }
func fptr(v float64) *float64 { return &v }

func TestPool_UpdateAndSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := NewPool(func() time.Time { return now })

	p.Update("dev-a", DeviceUpdate{
		Country:  "US",
		AvgRTTms: iptr(50),
		LossRate: fptr(0.0),
		SeenAt:   now,
	})
	p.Update("dev-b", DeviceUpdate{
		Country:  "US",
		AvgRTTms: iptr(30),
		LossRate: fptr(0.05),
		SeenAt:   now,
	})

	snap := p.Snapshot(map[string]struct{}{"dev-a": {}, "dev-b": {}})
	if len(snap) != 2 {
		t.Fatalf("snap len=%d, want 2", len(snap))
	}
	// Sorted by id.
	if snap[0].DeviceID != "dev-a" || snap[1].DeviceID != "dev-b" {
		t.Fatalf("order off: %+v", snap)
	}
	if snap[0].AvgRTT != 50 || snap[1].LossRate != 0.05 {
		t.Fatalf("metric mismatch: %+v", snap)
	}
}

func TestPool_NilPointersDontClobber(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := NewPool(func() time.Time { return now })
	p.Update("dev-a", DeviceUpdate{
		Country:  "US",
		AvgRTTms: iptr(50),
		LossRate: fptr(0.1),
		SeenAt:   now,
	})
	// Heartbeat without metrics (insufficient samples) must NOT zero them out.
	p.Update("dev-a", DeviceUpdate{Country: "US", SeenAt: now.Add(time.Second)})

	snap := p.Snapshot(map[string]struct{}{"dev-a": {}})
	if !snap[0].HasStats || snap[0].AvgRTT != 50 || snap[0].LossRate != 0.1 {
		t.Fatalf("metric clobbered: %+v", snap[0])
	}
}

func TestPool_OnlineFilterAndUnknown(t *testing.T) {
	p := NewPool(nil)
	p.Update("dev-a", DeviceUpdate{Country: "US", SeenAt: time.Now()})
	p.Update("dev-b", DeviceUpdate{Country: "DE", SeenAt: time.Now()})

	online := map[string]struct{}{"dev-a": {}, "dev-c": {}}
	snap := p.Snapshot(online)
	if len(snap) != 2 {
		t.Fatalf("expected 2 (a, c), got %d", len(snap))
	}
	// dev-b excluded; dev-c present even with no stats.
	for _, c := range snap {
		if c.DeviceID == "dev-b" {
			t.Fatal("dev-b should be filtered out")
		}
		if c.DeviceID == "dev-c" && c.HasStats {
			t.Fatal("dev-c should have HasStats=false (no heartbeat yet)")
		}
	}
}
