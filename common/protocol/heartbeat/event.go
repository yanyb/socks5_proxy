// Package heartbeat is the wire schema shared by the server (publisher) and
// the admin service (NSQ consumer). It is intentionally tiny and dependency-free
// so both sides marshal/unmarshal the exact same JSON.
package heartbeat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Topic is the default NSQ topic for heartbeat events. Override via config.
const Topic = "device.heartbeat"

// Event is the per-heartbeat record published to NSQ by the server and consumed
// by admin. Optional metrics use pointers so an explicit value of 0 is still
// transmitted while "not enough samples yet" is encoded as null/omitted.
//
// All timestamps are unix-milli (int64). Geo is best-effort: GeoNotFound (when
// the IP is private/unknown) yields a zero Geo, not an error in the publisher.
type Event struct {
	DeviceID string `json:"device_id"`
	// RemoteIP is the device's TCP source IP as observed by the server.
	// May be IPv4 or IPv6; "" if the server could not determine it.
	RemoteIP string `json:"remote_ip,omitempty"`
	// NetType is what the device self-reports ("wifi", "5g", "4g", ...).
	NetType string `json:"net_type,omitempty"`

	// CurTsMs is the device wall clock at heartbeat send (echoed in the
	// heartbeat_ack so the device can compute RTT). Helpful for cross-checking
	// drift on the admin side.
	CurTsMs int64 `json:"cur_ts_ms,omitempty"`
	// ServerRecvMs is the server's wall clock when the heartbeat arrived.
	ServerRecvMs int64 `json:"server_recv_ms"`

	// Optional sliding-window stats from the device (5-sample window).
	AvgRTTms *int64   `json:"avg_rtt_ms,omitempty"`
	LossRate *float64 `json:"loss_rate,omitempty"`

	// Geo is filled by the server using its local GeoLite2 DB.
	Geo Geo `json:"geo,omitempty"`
}

// Geo is the subset of GeoLite2-City fields we persist. All fields may be empty
// if the IP wasn't found in the database (private IP, ranges not yet covered).
type Geo struct {
	Country     string `json:"country,omitempty"`      // ISO-3166-1 alpha-2, e.g. "US"
	CountryName string `json:"country_name,omitempty"` // English long form
	Region      string `json:"region,omitempty"`       // first subdivision name (e.g. state/province)
	RegionCode  string `json:"region_code,omitempty"`  // ISO-3166-2 subdivision code
	City        string `json:"city,omitempty"`
}

// IsZero reports whether g has no useful info.
func (g Geo) IsZero() bool { return g == Geo{} }

// MarshalJSON omits the geo object entirely when it's zero, so the wire stays
// "geo":<missing> rather than "geo":{} (smaller messages, fewer empty docs).
func (g Geo) MarshalJSON() ([]byte, error) {
	if g.IsZero() {
		return []byte("null"), nil
	}
	type alias Geo
	return json.Marshal(alias(g))
}

// UnmarshalJSON accepts both null and a normal object.
func (g *Geo) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*g = Geo{}
		return nil
	}
	type alias Geo
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("geo: %w", err)
	}
	*g = Geo(a)
	return nil
}
