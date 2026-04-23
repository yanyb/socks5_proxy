// Package geo wraps the MaxMind GeoLite2-City reader. The hot path is one
// in-memory mmap lookup per heartbeat (~µs); ops side reloads the file by
// dropping a new .mmdb in place and sending SIGHUP.
package geo

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"

	"xsocks5/protocol/heartbeat"

	"github.com/oschwald/geoip2-golang"
)

// Lookuper is the seam exposed to the rest of the server. Real impl uses
// GeoLite2; tests can substitute a static map.
type Lookuper interface {
	Lookup(ip net.IP) heartbeat.Geo
}

// LookuperFunc adapts a plain function to Lookuper.
type LookuperFunc func(net.IP) heartbeat.Geo

// Lookup implements Lookuper.
func (f LookuperFunc) Lookup(ip net.IP) heartbeat.Geo { return f(ip) }

// NopLookuper returns zero geo for any IP. Useful when GeoIP DB is unconfigured.
var NopLookuper Lookuper = LookuperFunc(func(net.IP) heartbeat.Geo { return heartbeat.Geo{} })

// GeoLite2 is a hot-reloadable wrapper around *geoip2.Reader. The reader is
// stored in atomic.Pointer so concurrent Lookup never races with Reload.
type GeoLite2 struct {
	path   string
	reader atomic.Pointer[geoip2.Reader]
}

// Open opens the MMDB at path and returns a *GeoLite2.
//
// Returns an error if the file is missing or unreadable; we want startup to
// loudly fail rather than silently lose geo info.
func Open(path string) (*GeoLite2, error) {
	if path == "" {
		return nil, errors.New("geo: db path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("geo: stat %q: %w", path, err)
	}
	r, err := geoip2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geo: open %q: %w", path, err)
	}
	g := &GeoLite2{path: path}
	g.reader.Store(r)
	return g, nil
}

// Path returns the configured DB path (handy for ops logging and SIGHUP).
func (g *GeoLite2) Path() string { return g.path }

// Reload re-opens the DB at the same path and atomically swaps the active
// reader. Safe to call concurrently with Lookup. Old reader is closed only
// after the swap so in-flight callers stay valid.
func (g *GeoLite2) Reload() error {
	r, err := geoip2.Open(g.path)
	if err != nil {
		return fmt.Errorf("geo: reload %q: %w", g.path, err)
	}
	old := g.reader.Swap(r)
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Close releases the underlying reader. Safe to call multiple times.
func (g *GeoLite2) Close() error {
	r := g.reader.Swap(nil)
	if r == nil {
		return nil
	}
	return r.Close()
}

// Lookup resolves an IP to a heartbeat.Geo. Private/unknown IPs and lookup
// errors return a zero Geo (caller treats it as "no info" rather than failure).
func (g *GeoLite2) Lookup(ip net.IP) heartbeat.Geo {
	if g == nil || ip == nil {
		return heartbeat.Geo{}
	}
	r := g.reader.Load()
	if r == nil {
		return heartbeat.Geo{}
	}
	rec, err := r.City(ip)
	if err != nil || rec == nil {
		return heartbeat.Geo{}
	}
	out := heartbeat.Geo{
		Country:     rec.Country.IsoCode,
		CountryName: rec.Country.Names["en"],
		City:        rec.City.Names["en"],
	}
	if len(rec.Subdivisions) > 0 {
		out.Region = rec.Subdivisions[0].Names["en"]
		out.RegionCode = rec.Subdivisions[0].IsoCode
	}
	return out
}
