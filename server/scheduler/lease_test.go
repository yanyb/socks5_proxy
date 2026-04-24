package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestLease_AcquireAndExclusivity(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	sl := NewLeases(func() time.Time { return clock })

	_, h1, err := sl.Acquire("user-a", "dev-1", "US", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer h1.Close()

	if _, _, err := sl.Acquire("user-b", "dev-1", "US", 5*time.Minute); !errors.Is(err, ErrDeviceTaken) {
		t.Fatalf("user-b should be denied dev-1, got %v", err)
	}
	// Same user can re-acquire its own device (idempotent).
	if _, _, err := sl.Acquire("user-a", "dev-1", "US", 5*time.Minute); err != nil {
		t.Fatalf("user-a re-acquire failed: %v", err)
	}
}

func TestLease_ExpiryFreesDevice(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sl := NewLeases(func() time.Time { return clock })

	_, h, _ := sl.Acquire("user-a", "dev-1", "", 5*time.Minute)
	_ = h.Close()

	// Right at expiry boundary -> still considered expired (Before, not BeforeOrEqual).
	clock = clock.Add(5 * time.Minute)
	if l := sl.GetActive("user-a"); l != nil {
		t.Fatalf("lease should be expired, got %+v", l)
	}
	// Different user can now grab dev-1.
	if _, _, err := sl.Acquire("user-b", "dev-1", "", 5*time.Minute); err != nil {
		t.Fatalf("dev-1 should be free after expiry, got %v", err)
	}
}

func TestLease_HeldDeviceIDs_ExcludesSelf(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sl := NewLeases(func() time.Time { return clock })

	_, _, _ = sl.Acquire("user-a", "dev-1", "", 5*time.Minute)
	_, _, _ = sl.Acquire("user-b", "dev-2", "", 5*time.Minute)

	heldFromA := sl.HeldDeviceIDs("user-a")
	if _, taken := heldFromA["dev-1"]; taken {
		t.Fatal("user-a's view should NOT include dev-1 (it owns it)")
	}
	if _, taken := heldFromA["dev-2"]; !taken {
		t.Fatal("user-a's view should include dev-2 (held by user-b)")
	}
}

func TestLease_HandleClose_Idempotent(t *testing.T) {
	sl := NewLeases(nil)
	_, h, _ := sl.Acquire("u", "d", "", time.Minute)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close must not panic / error.
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLease_Sweep(t *testing.T) {
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sl := NewLeases(func() time.Time { return clock })
	_, _, _ = sl.Acquire("u1", "d1", "", time.Minute)
	_, _, _ = sl.Acquire("u2", "d2", "", time.Hour)
	clock = clock.Add(2 * time.Minute)

	if n := sl.Sweep(); n != 1 {
		t.Fatalf("sweep evicted %d, want 1", n)
	}
	if sl.Size() != 1 {
		t.Fatalf("size after sweep=%d, want 1", sl.Size())
	}
}
