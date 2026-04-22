// Package mobile is the gomobile bind target for Android: Go device client + Java HostResolver for system DNS.
package mobile

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"xsocks5/client/config"
	"xsocks5/client/core"
)

// HostResolver is implemented in Java/Kotlin. Use Android APIs such as InetAddress.getAllByName
// or android.net.DnsResolver so resolution follows the device's network and Private DNS settings.
//
// LookupHost returns newline-separated IP address strings (gobind cannot expose []string from
// Java implementations; see golang.org/x/mobile/cmd/gobind type restrictions).
type HostResolver interface {
	LookupHost(hostname string) (string, error)
}

// ClientConfig holds device client settings (primitive / duration-as-ns fields for gomobile).
type ClientConfig struct {
	DeviceID                  string
	Token                     string
	ServerAddr                string
	HeartbeatIntervalNs       int64
	ReconnectInitialBackoffNs int64
	ReconnectMaxBackoffNs     int64
}

var stderrLog = log.New(os.Stderr, "device: ", log.LstdFlags)

var runState struct {
	sync.Mutex
	cancel context.CancelFunc
}

// Run starts the TLS+yamux device client and blocks until [Stop] is called, context is cancelled, or a fatal error.
// Only one Run may be active at a time.
//
// The signature intentionally does not use context.Context: gobind cannot bind standard-library context.Context,
// so gomobile would omit Run from the generated Java API entirely.
//
// On Android, pass a non-nil HostResolver so the server hostname is resolved with system DNS before TCP dial.
// TLS SNI still uses the hostname from ServerAddr. Pass nil for resolver to use Go's default resolver (e.g. tests on desktop).
func Run(cfg *ClientConfig, resolver HostResolver) error {
	c, err := parseClientCfg(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())

	runState.Lock()
	if runState.cancel != nil {
		runState.Unlock()
		cancel()
		return fmt.Errorf("mobile: Run already in progress")
	}
	runState.cancel = cancel
	runState.Unlock()

	defer func() {
		runState.Lock()
		runState.cancel = nil
		runState.Unlock()
		cancel()
	}()

	if resolver == nil {
		return core.Run(ctx, c, stderrLog)
	}
	return core.RunWithHostLookup(ctx, c, stderrLog, func(ctx context.Context, host string) ([]string, error) {
		s, err := resolver.LookupHost(host)
		if err != nil {
			return nil, err
		}
		return core.ParseAddrsFromLookupString(s), nil
	})
}

// Stop cancels a blocking [Run] from another thread (e.g. Android Activity onDestroy).
func Stop() {
	runState.Lock()
	c := runState.cancel
	runState.Unlock()
	if c != nil {
		c()
	}
}

func parseClientCfg(cfg *ClientConfig) (*config.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cfg is nil")
	}
	if cfg.DeviceID == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	if cfg.ServerAddr == "" {
		return nil, fmt.Errorf("server_addr is required")
	}
	c := &config.Client{
		DeviceID:                cfg.DeviceID,
		Token:                   cfg.Token,
		ServerAddr:              cfg.ServerAddr,
		HeartbeatInterval:       time.Duration(cfg.HeartbeatIntervalNs),
		ReconnectInitialBackoff: time.Duration(cfg.ReconnectInitialBackoffNs),
		ReconnectMaxBackoff:     time.Duration(cfg.ReconnectMaxBackoffNs),
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 30 * time.Second
	}
	if c.ReconnectInitialBackoff == 0 {
		c.ReconnectInitialBackoff = time.Second
	}
	if c.ReconnectMaxBackoff == 0 {
		c.ReconnectMaxBackoff = 60 * time.Second
	}
	if c.ReconnectMaxBackoff < c.ReconnectInitialBackoff {
		c.ReconnectMaxBackoff = c.ReconnectInitialBackoff
	}
	return c, nil
}
