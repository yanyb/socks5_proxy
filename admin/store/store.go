// Package store persists registered devices. The DeviceStore interface lets
// the rest of admin be tested without Mongo.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DeviceInfo is the plaintext device info uploaded by /register.
type DeviceInfo struct {
	OS           string
	Brand        string
	Model        string
	IMEI         string
	AndroidID    string
	SerialNumber string
	// ClientTimestampMs is the device wall clock in JSON A.
	ClientTimestampMs int64
	// RemoteAddr is the HTTP source address (set by the handler).
	RemoteAddr string
}

// Device is the persisted record returned by UpsertDevice.
type Device struct {
	DeviceID            string
	Fingerprint         string
	OS                  string
	Brand               string
	Model               string
	IMEI                string
	AndroidID           string
	SerialNumber        string
	FirstRegisterAt     time.Time
	LastRegisterAt      time.Time
	LastClientTimestamp int64
	LastRemoteAddr      string
	RegisterCount       int64
}

// NetworkSnapshot is the latest "live" view of a device's network as observed
// by the server (geo) and reported by the device itself (rtt, loss, net_type).
// Pointer fields are optional: nil means "device hasn't reported yet"; we never
// overwrite a known value with nil (see Mongo $set semantics in the impl).
type NetworkSnapshot struct {
	LastIP       string
	NetType      string
	AvgRTTms     *int64
	LossRate     *float64
	Country      string
	CountryName  string
	Region       string
	RegionCode   string
	City         string
	CurTsMs      int64 // device wall clock at heartbeat send
	ServerRecvMs int64 // server wall clock when heartbeat arrived
}

// DeviceStore is the persistence seam for /register and the heartbeat consumer.
//
// UpsertDevice should:
//  1. Compute a deterministic fingerprint from info (so the same physical
//     device gets the same DeviceID across re-registrations).
//  2. find_one_and_update with upsert; on insert allocate a fresh DeviceID.
//  3. Always set LastRegisterAt / LastClientTimestamp / LastRemoteAddr;
//     increment RegisterCount.
//
// UpdateDeviceNetwork merges a NetworkSnapshot into the device document.
// It MUST be a no-op if the device_id is unknown (the consumer logs and
// drops the message, since heartbeats from never-registered devices are a
// data anomaly we don't want to silently create rows for).
type DeviceStore interface {
	UpsertDevice(ctx context.Context, info DeviceInfo) (*Device, error)
	UpdateDeviceNetwork(ctx context.Context, deviceID string, snap NetworkSnapshot) error
	Close(ctx context.Context) error
}

// ErrUnknownDevice is returned by UpdateDeviceNetwork when no document
// matches the given DeviceID. Callers (NSQ consumer) treat it as a soft
// drop, not a redelivery condition.
var ErrUnknownDevice = errors.New("store: unknown device_id")

// Fingerprint chooses the strongest available identifier. The order matches
// what manufacturers tend to keep stable across factory resets / OS upgrades.
// Returns "" when no identifier is available; the caller will then allocate
// a DeviceID without dedup (each call inserts a new doc).
func Fingerprint(info DeviceInfo) string {
	for _, s := range []string{info.AndroidID, info.IMEI, info.SerialNumber} {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

// NewDeviceID returns a fresh "dev-XXXXXXXXXXXX" id (12 hex chars, 48 bits).
// Collisions are extremely unlikely; the Mongo unique index is the source of truth.
func NewDeviceID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("device id: %w", err)
	}
	return "dev-" + hex.EncodeToString(b[:]), nil
}
