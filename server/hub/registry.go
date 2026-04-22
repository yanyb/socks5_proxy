package hub

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Registry maps device_id -> active yamux session (last registration wins).
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*yamux.Session
}

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*yamux.Session),
	}
}

// ListOnline returns sorted device_ids that currently have an active session.
func (r *Registry) ListOnline() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// OnlineCount returns how many devices are connected.
func (r *Registry) OnlineCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// ResolveDeviceForDial decides which device_id to use for this SOCKS connection.
// socksUser is the SOCKS5 username when user/password auth is enabled — it must be the device_id.
// With no username (no-auth mode), only one device may be online; otherwise the user must use auth to pick a device.
func (r *Registry) ResolveDeviceForDial(socksUser string) (string, error) {
	u := strings.TrimSpace(socksUser)
	if u != "" {
		return u, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.sessions) == 0 {
		return "", fmt.Errorf("no device online")
	}
	if len(r.sessions) == 1 {
		for id := range r.sessions {
			return id, nil
		}
	}
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return "", fmt.Errorf("multiple devices online %v: enable socks_auth_password and set SOCKS5 username to a device_id", ids)
}

func (r *Registry) Put(deviceID string, sess *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.sessions[deviceID]; ok && old != nil {
		_ = old.Close()
	}
	r.sessions[deviceID] = sess
}

func (r *Registry) Remove(deviceID string, sess *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.sessions[deviceID]; ok && cur == sess {
		delete(r.sessions, deviceID)
	}
}

func (r *Registry) Get(deviceID string) (*yamux.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[deviceID]
	return s, ok
}

// CloseAll closes every active yamux session. It does not delete entries from the
// map: each ServeDevice goroutine is responsible for calling Remove via its own
// deferred cleanup once its session shuts down. Safe to call concurrently with
// Put/Remove/Get.
func (r *Registry) CloseAll() {
	r.mu.RLock()
	sessions := make([]*yamux.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.RUnlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}

// WaitSession waits until wantID is online or ctx is cancelled.
func (r *Registry) WaitSession(ctx context.Context, wantID string) (*yamux.Session, string, error) {
	wantID = strings.TrimSpace(wantID)
	if wantID == "" {
		return nil, "", fmt.Errorf("empty device id")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s, id, ok := r.trySession(wantID); ok {
			return s, id, nil
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Registry) trySession(wantID string) (*yamux.Session, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[wantID]
	if !ok {
		return nil, "", false
	}
	return s, wantID, true
}
