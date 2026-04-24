package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

type staticLister struct{ ids map[string]struct{} }

func (s staticLister) OnlineSet() map[string]struct{} {
	out := make(map[string]struct{}, len(s.ids))
	for k := range s.ids {
		out[k] = struct{}{}
	}
	return out
}

func newSched(t *testing.T, online []string, now func() time.Time) *Scheduler {
	t.Helper()
	ids := make(map[string]struct{}, len(online))
	for _, id := range online {
		ids[id] = struct{}{}
	}
	pool := NewPool(now)
	leases := NewLeases(now)
	s, err := New(Config{
		Lister: staticLister{ids: ids},
		Pool:   pool,
		Leases: leases,
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSchedulerPick_StickyReuse(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	s := newSched(t, []string{"dev-a", "dev-b"}, now)
	s.Pool.Update("dev-a", DeviceUpdate{Country: "US", AvgRTTms: iptr(10), SeenAt: clock})
	s.Pool.Update("dev-b", DeviceUpdate{Country: "US", AvgRTTms: iptr(50), SeenAt: clock})

	dev1, h1, err := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	if err != nil {
		t.Fatal(err)
	}
	defer h1.Close()
	if dev1 != "dev-a" {
		t.Fatalf("first pick=%q, want dev-a", dev1)
	}
	// Same user, second request: must reuse dev-a even if its score got worse.
	s.Pool.Update("dev-a", DeviceUpdate{Country: "US", AvgRTTms: iptr(500), SeenAt: clock})
	dev2, h2, err := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if dev2 != "dev-a" {
		t.Fatalf("sticky reuse expected dev-a, got %q", dev2)
	}
}

func TestSchedulerPick_DifferentUsers_DifferentDevices(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newSched(t, []string{"dev-a", "dev-b"}, func() time.Time { return clock })
	s.Pool.Update("dev-a", DeviceUpdate{Country: "US", AvgRTTms: iptr(10), SeenAt: clock})
	s.Pool.Update("dev-b", DeviceUpdate{Country: "US", AvgRTTms: iptr(50), SeenAt: clock})

	d1, h1, _ := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	defer h1.Close()
	d2, h2, err := s.Pick(context.Background(), "B_2_US_5_Ab000002")
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if d1 == d2 {
		t.Fatalf("two users got same device %q (must be exclusive)", d1)
	}
}

func TestSchedulerPick_StickyDeviceWentOffline_Repicks(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	online := map[string]struct{}{"dev-a": {}, "dev-b": {}}
	pool := NewPool(now)
	leases := NewLeases(now)
	s, _ := New(Config{
		Lister: dynLister{m: &online},
		Pool:   pool, Leases: leases, Now: now,
	})
	pool.Update("dev-a", DeviceUpdate{Country: "US", AvgRTTms: iptr(10), SeenAt: clock})
	pool.Update("dev-b", DeviceUpdate{Country: "US", AvgRTTms: iptr(50), SeenAt: clock})

	d1, h1, _ := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	if d1 != "dev-a" {
		t.Fatalf("first pick=%q, want dev-a", d1)
	}
	_ = h1.Close()

	// dev-a goes offline; same user must be repicked to dev-b.
	delete(online, "dev-a")
	d2, h2, err := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if d2 != "dev-b" {
		t.Fatalf("repick=%q, want dev-b", d2)
	}
}

func TestSchedulerPick_BadUsername(t *testing.T) {
	s := newSched(t, []string{"dev-a"}, time.Now)
	_, _, err := s.Pick(context.Background(), "not-a-username")
	if !errors.Is(err, ErrBadUsername) {
		t.Fatalf("want ErrBadUsername, got %v", err)
	}
}

func TestSchedulerPick_NoDeviceOnline(t *testing.T) {
	s := newSched(t, nil, time.Now)
	_, _, err := s.Pick(context.Background(), "B_1_US_5_Ab000001")
	if !errors.Is(err, ErrNoDevice) {
		t.Fatalf("want ErrNoDevice, got %v", err)
	}
}

// dynLister exposes a map by reference so tests can mutate online status.
type dynLister struct{ m *map[string]struct{} }

func (d dynLister) OnlineSet() map[string]struct{} {
	out := make(map[string]struct{}, len(*d.m))
	for k := range *d.m {
		out[k] = struct{}{}
	}
	return out
}
