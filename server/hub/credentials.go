package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// CredentialSource is the seam between the SOCKS5 server and the upstream truth
// of "which usernames + passwords are allowed". Today this is fronted by a JSON
// file; later it will be backed by the admin service (HTTP/gRPC/DB).
//
// FetchAll returns the complete current snapshot. Implementations should be
// safe to call concurrently. Returning an error makes CredentialCache keep its
// previous snapshot — a transient admin outage must not lock everyone out.
type CredentialSource interface {
	FetchAll(ctx context.Context) (map[string]string, error)
}

// CredentialCache wraps a CredentialSource with an in-memory snapshot that is
// refreshed on a fixed interval. SOCKS5 Valid() lookups hit the cache only;
// they never touch the source. This is what go-socks5 calls per request, so it
// must stay O(1) and lock-light.
type CredentialCache struct {
	src             CredentialSource
	refreshInterval time.Duration
	logger          *logrus.Logger

	mu    sync.RWMutex
	creds map[string]string
}

// NewCredentialCache constructs a cache. refresh<=0 falls back to 1 minute.
func NewCredentialCache(src CredentialSource, refresh time.Duration, logger *logrus.Logger) *CredentialCache {
	if refresh <= 0 {
		refresh = time.Minute
	}
	return &CredentialCache{
		src:             src,
		refreshInterval: refresh,
		logger:          logger,
		creds:           make(map[string]string),
	}
}

// Start performs an initial blocking Refresh, then runs periodic refreshes in a
// goroutine until ctx is cancelled. Returning an error from the initial load
// lets the caller decide to abort startup (recommended).
func (c *CredentialCache) Start(ctx context.Context) error {
	if err := c.Refresh(ctx); err != nil {
		return fmt.Errorf("initial credentials load: %w", err)
	}
	c.logger.WithFields(logrus.Fields{
		"component":     "credentials",
		"user_count":    c.Size(),
		"refresh_every": c.refreshInterval.String(),
	}).Info("credentials cache loaded")

	go c.refreshLoop(ctx)
	return nil
}

// Refresh fetches the latest snapshot and atomically replaces the cache.
// On error the cache is left untouched.
func (c *CredentialCache) Refresh(ctx context.Context) error {
	next, err := c.src.FetchAll(ctx)
	if err != nil {
		return err
	}
	if next == nil {
		next = make(map[string]string)
	}
	c.mu.Lock()
	c.creds = next
	c.mu.Unlock()
	return nil
}

// Size is the current number of cached entries. Safe to call concurrently.
func (c *CredentialCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.creds)
}

// Valid implements socks5.CredentialStore. The compare is constant-time to
// avoid leaking password length / prefix info via timing.
func (c *CredentialCache) Valid(user, password, _ string) bool {
	user = strings.TrimSpace(user)
	if user == "" || password == "" {
		return false
	}
	c.mu.RLock()
	want, ok := c.creds[user]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(want)) == 1
}

func (c *CredentialCache) refreshLoop(ctx context.Context) {
	log := c.logger.WithField("component", "credentials")
	t := time.NewTicker(c.refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Debug("refresh loop stopped")
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, c.refreshInterval)
			err := c.Refresh(rctx)
			cancel()
			if err != nil {
				// Keep previous snapshot; transient admin failure must not lock users out.
				log.WithError(err).Warn("refresh failed; keeping previous snapshot")
				continue
			}
			log.WithField("user_count", c.Size()).Debug("cache refreshed")
		}
	}
}

// JSONFileCredentialSource reads a JSON object {"user": "password", ...} from
// disk. Use this as a placeholder before the admin service exists, or for
// air-gapped deployments.
type JSONFileCredentialSource struct {
	Path string
}

// FetchAll re-reads the file every time so editing it on disk is picked up by
// the next refresh tick.
func (s *JSONFileCredentialSource) FetchAll(_ context.Context) (map[string]string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	creds := make(map[string]string)
	if len(data) == 0 {
		return creds, nil
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	return creds, nil
}
