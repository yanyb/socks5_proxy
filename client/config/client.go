// Package config holds device client settings parsed from YAML/JSON.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Client is device-side settings only. Unmarshaled from client/configs/client.yaml or JSON from your control plane.
// TLS to the server does not verify the server certificate (trust-on-first-use style); encryption still applies.
type Client struct {
	DeviceID          string        `yaml:"device_id" json:"device_id"`
	ServerAddr        string        `yaml:"server_addr" json:"server_addr"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval" json:"heartbeat_interval"`
	// Reconnect backoff after TCP/TLS/yamux failure or session drop (exponential, capped).
	ReconnectInitialBackoff time.Duration `yaml:"reconnect_initial_backoff" json:"reconnect_initial_backoff"`
	ReconnectMaxBackoff     time.Duration `yaml:"reconnect_max_backoff" json:"reconnect_max_backoff"`

	// HeartbeatStats is an optional shared stats tracker so callers (e.g. the
	// mobile bridge) can update NetType from outside the run loop. If nil,
	// the loop creates a private one with the default 5-sample window.
	// Type kept as `any` here to avoid an import cycle with client/core; the
	// loop type-asserts to *core.HeartbeatStats.
	HeartbeatStats HeartbeatStatsInterface `yaml:"-" json:"-"`
}

// HeartbeatStatsInterface is the minimum surface the heartbeat loop needs.
// client/core.HeartbeatStats satisfies it.
type HeartbeatStatsInterface interface {
	NetType() string
	Sent()
	AckOK(rttMs int64)
	AckLost()
	SnapshotForSend() (avgRTTms *int64, lossRate *float64)
}

// LoadClient reads a client-only config file. Use .json for JSON (e.g. file saved from control-plane API); otherwise YAML is assumed.
func LoadClient(path string) (*Client, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if isJSONPath(path) {
		return ParseClientJSON(data)
	}
	return ParseClientYAML(data)
}

// ParseClientYAML parses device config from YAML bytes.
func ParseClientYAML(data []byte) (*Client, error) {
	var c Client
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return validateClient(&c)
}

// ParseClientJSON parses device config from JSON bytes (e.g. control plane API response).
// heartbeat_interval may be a string like "30s" (recommended for APIs) or a number (nanoseconds).
func ParseClientJSON(data []byte) (*Client, error) {
	var wire struct {
		DeviceID                string          `json:"device_id"`
		ServerAddr              string          `json:"server_addr"`
		HeartbeatInterval       json.RawMessage `json:"heartbeat_interval"`
		ReconnectInitialBackoff json.RawMessage `json:"reconnect_initial_backoff"`
		ReconnectMaxBackoff     json.RawMessage `json:"reconnect_max_backoff"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	c := &Client{
		DeviceID:   wire.DeviceID,
		ServerAddr: wire.ServerAddr,
	}
	if d, err := parseJSONDurationField(wire.HeartbeatInterval, "heartbeat_interval"); err != nil {
		return nil, err
	} else {
		c.HeartbeatInterval = d
	}
	if d, err := parseJSONDurationField(wire.ReconnectInitialBackoff, "reconnect_initial_backoff"); err != nil {
		return nil, err
	} else {
		c.ReconnectInitialBackoff = d
	}
	if d, err := parseJSONDurationField(wire.ReconnectMaxBackoff, "reconnect_max_backoff"); err != nil {
		return nil, err
	} else {
		c.ReconnectMaxBackoff = d
	}
	return validateClient(c)
}

func validateClient(c *Client) (*Client, error) {
	if c.DeviceID == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	if c.ServerAddr == "" {
		return nil, fmt.Errorf("server_addr is required")
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

func isJSONPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}

func parseJSONDurationField(raw json.RawMessage, name string) (time.Duration, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		return d, nil
	}
	var ns int64
	if err := json.Unmarshal(raw, &ns); err != nil {
		return 0, fmt.Errorf("%s: expected string duration or int64 nanoseconds", name)
	}
	return time.Duration(ns), nil
}
