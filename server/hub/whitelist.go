package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/things-go/go-socks5"
)

// IPWhitelistSource returns the current allowed client CIDRs as strings.
// Each string is either a CIDR ("192.0.2.0/24", "2001:db8::/32") or a single
// host IP, which is treated as /32 or /128. Implementations may be backed by
// an admin HTTP API, a local file, a database, etc. Fetch is called from
// the refresh loop; it should be safe for concurrent use.
type IPWhitelistSource interface {
	FetchCIDRs(ctx context.Context) ([]string, error)
}

// IPWhitelistCache holds a parsed CIDR set refreshed from an IPWhitelistSource
// on a fixed interval. Lookups (RuleSet) hit memory only, never the source.
type IPWhitelistCache struct {
	src             IPWhitelistSource
	refreshInterval time.Duration
	logger          *logrus.Logger

	mu   sync.RWMutex
	nets []*net.IPNet
}

// NewIPWhitelistCache constructs a cache. refresh <= 0 defaults to 1 minute.
func NewIPWhitelistCache(src IPWhitelistSource, refresh time.Duration, log *logrus.Logger) *IPWhitelistCache {
	if refresh <= 0 {
		refresh = time.Minute
	}
	return &IPWhitelistCache{
		src:             src,
		refreshInterval: refresh,
		logger:          log,
	}
}

// Start does an initial Refresh then periodic refreshes until ctx is done.
// The initial error is returned so the caller can abort boot on misconfiguration.
func (c *IPWhitelistCache) Start(ctx context.Context) error {
	if err := c.Refresh(ctx); err != nil {
		return fmt.Errorf("initial IP whitelist: %w", err)
	}
	if c.logger != nil {
		c.logger.WithFields(logrus.Fields{
			"component":     "ip_whitelist",
			"cidr_count":    c.Count(),
			"refresh_every": c.refreshInterval.String(),
		}).Info("ip whitelist cache loaded")
	}
	go c.refreshLoop(ctx)
	return nil
}

func (c *IPWhitelistCache) countLocked() int {
	return len(c.nets)
}

// Count returns the number of CIDR entries in the current snapshot.
func (c *IPWhitelistCache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nets)
}

// Refresh fetches the latest CIDR list and atomically replaces the in-memory
// set. On error, the previous snapshot is kept.
func (c *IPWhitelistCache) Refresh(ctx context.Context) error {
	lines, err := c.src.FetchCIDRs(ctx)
	if err != nil {
		return err
	}
	parsed, err := parseCIDRStringList(lines)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.nets = parsed
	c.mu.Unlock()
	return nil
}

// Allows reports whether the client address is within any whitelisted CIDR.
func (c *IPWhitelistCache) Allows(client net.IP) bool {
	if c == nil || client == nil {
		return false
	}
	if v4 := client.To4(); v4 != nil {
		client = v4
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.nets {
		if n != nil && n.Contains(client) {
			return true
		}
	}
	return false
}

func (c *IPWhitelistCache) refreshLoop(ctx context.Context) {
	log := c.logger.WithField("component", "ip_whitelist")
	t := time.NewTicker(c.refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if log != nil {
				log.Debug("ip whitelist refresh loop stopped")
			}
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, c.refreshInterval)
			err := c.Refresh(rctx)
			cancel()
			if err != nil {
				if log != nil {
					log.WithError(err).Warn("ip whitelist refresh failed; keeping previous snapshot")
				}
				continue
			}
			if log != nil {
				log.WithField("cidr_count", c.Count()).Debug("ip whitelist cache refreshed")
			}
		}
	}
}

// IPRuleSet enforces a client IP allowlist, then defers to Inner (e.g. command permits).
// Implements socks5.RuleSet.
type IPRuleSet struct {
	Whitelist *IPWhitelistCache
	Inner     socks5.RuleSet
	Log       *logrus.Logger
}

// NewIPRuleSet returns a non-nil *IPRuleSet. If Inner is nil, clients that
// pass the allowlist are permitted (use explicit Inner such as *PermitCommand
// in production).
func NewIPRuleSet(whitelist *IPWhitelistCache, inner socks5.RuleSet, log *logrus.Logger) *IPRuleSet {
	if whitelist == nil {
		panic("ip whitelist: NewIPRuleSet: whitelist is nil")
	}
	return &IPRuleSet{Whitelist: whitelist, Inner: inner, Log: log}
}

// Allow implements socks5.RuleSet.
func (r *IPRuleSet) Allow(ctx context.Context, req *socks5.Request) (context.Context, bool) {
	if req == nil {
		return ctx, false
	}
	ip := ClientIPFromSocksRequest(req)
	if !r.Whitelist.Allows(ip) {
		if r.Log != nil {
			r.Log.WithField("client_ip", formatIPForLog(ip)).Info("socks5 client not in IP whitelist")
		}
		return ctx, false
	}
	if r.Inner != nil {
		return r.Inner.Allow(ctx, req)
	}
	return ctx, true
}

// ClientIPFromSocksRequest returns the client IP (without port) for SOCKS5 rules.
// Returns nil if the address cannot be parsed to an IP.
func ClientIPFromSocksRequest(req *socks5.Request) net.IP {
	if req == nil || req.RemoteAddr == nil {
		return nil
	}
	return HostIPFromAddr(req.RemoteAddr)
}

// HostIPFromAddr returns the host IP for a net.Addr that is typically host:port.
func HostIPFromAddr(a net.Addr) net.IP {
	if a == nil {
		return nil
	}
	if ta, ok := a.(*net.TCPAddr); ok {
		if ta.IP != nil {
			if v4 := ta.IP.To4(); v4 != nil {
				return v4
			}
			return copyIP(ta.IP)
		}
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		if ip := net.ParseIP(a.String()); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4
			}
			return copyIP(ip)
		}
		return nil
	}
	if ip := net.ParseIP(h); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4
		}
		return copyIP(ip)
	}
	return nil
}

func copyIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}

func formatIPForLog(ip net.IP) string {
	if ip == nil {
		return "<nil>"
	}
	return ip.String()
}

// parseCIDRStringList normalizes a list of CIDRs or host IPs to []*IPNet.
func parseCIDRStringList(lines []string) ([]*net.IPNet, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	var out []*net.IPNet
	for i, s := range lines {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(s)
		if err == nil {
			out = append(out, cidr)
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("whitelist line %d: not a valid CIDR or IP: %q", i+1, s)
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)})
			continue
		}
		out = append(out, &net.IPNet{IP: copyIP(ip), Mask: net.CIDRMask(128, 128)})
	}
	return out, nil
}

// ——— concrete IPWhitelistSource implementations ———

// JSONFileIPWhitelistSource loads a JSON array of CIDR strings, e.g.
//
//	["10.0.0.0/8", "192.168.0.0/16", "203.0.113.1"]
//
// Each read hits disk so a future refresh can pick up edits. Replace with
// an admin HTTP source when the API is ready.
type JSONFileIPWhitelistSource struct {
	Path string
}

// FetchCIDRs reads the file on every call (as with credentials file).
func (s *JSONFileIPWhitelistSource) FetchCIDRs(_ context.Context) ([]string, error) {
	if strings.TrimSpace(s.Path) == "" {
		return nil, fmt.Errorf("ip whitelist: json file path is empty")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, nil
	}
	var cidrList []string
	if err := json.Unmarshal(data, &cidrList); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	return cidrList, nil
}

// URLJSONIPWhitelistSource fetches a JSON array of CIDR strings from a URL
// (e.g. an admin service). GET with optional query string in URL.
// Suitable for: https://admin.internal/api/socks/whitelist?token=…
type URLJSONIPWhitelistSource struct {
	URL    string
	Client *http.Client
}

// FetchCIDRs issues GET to URL and decodes a JSON string array.
func (s *URLJSONIPWhitelistSource) FetchCIDRs(ctx context.Context) ([]string, error) {
	u := strings.TrimSpace(s.URL)
	if u == "" {
		return nil, fmt.Errorf("ip whitelist: source URL is empty")
	}
	if _, err := url.ParseRequestURI(u); err != nil {
		return nil, fmt.Errorf("ip whitelist: bad URL: %w", err)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("ip whitelist: GET %s: status %d", u, res.StatusCode)
	}
	var cidrList []string
	if err := json.NewDecoder(res.Body).Decode(&cidrList); err != nil {
		return nil, err
	}
	return cidrList, nil
}

// StaticCIDRsSource is a IPWhitelistSource that always returns a fixed list.
// Useful in tests and small single-instance deployments.
type StaticCIDRsSource struct {
	CIDRs []string
}

// FetchCIDRs returns a copy of the static list.
func (s *StaticCIDRsSource) FetchCIDRs(_ context.Context) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	out := make([]string, len(s.CIDRs))
	copy(out, s.CIDRs)
	return out, nil
}
