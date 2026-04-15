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

// Server is cloud-side settings only. Unmarshaled from configs/server.yaml (or equivalent API payload).
type Server struct {
	SocksListen     string `yaml:"socks_listen" json:"socks_listen"`
	DeviceListen    string `yaml:"device_listen" json:"device_listen"`
	TLSCertFile     string `yaml:"tls_cert_file" json:"tls_cert_file"`
	TLSKeyFile string `yaml:"tls_key_file" json:"tls_key_file"`
	// SocksAuthPassword enables RFC SOCKS5 user/pass auth. Username must be the target device_id; password is this shared secret. Recommended when multiple phones are online.
	SocksAuthPassword       string        `yaml:"socks_auth_password" json:"socks_auth_password"`
	SessionHeartbeatTimeout time.Duration `yaml:"session_heartbeat_timeout" json:"session_heartbeat_timeout"`
	DeviceWaitTimeout       time.Duration `yaml:"device_wait_timeout" json:"device_wait_timeout"`
	ConnectResultTimeout    time.Duration `yaml:"connect_result_timeout" json:"connect_result_timeout"`
}

// Client is device-side settings only. Unmarshaled from configs/client.yaml or JSON from your control plane.
// TLS to the server does not verify the server certificate (trust-on-first-use style); encryption still applies.
type Client struct {
	DeviceID          string        `yaml:"device_id" json:"device_id"`
	Token             string        `yaml:"token" json:"token"`
	ServerAddr        string        `yaml:"server_addr" json:"server_addr"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval" json:"heartbeat_interval"`
	// Reconnect backoff after TCP/TLS/yamux failure or session drop (exponential, capped).
	ReconnectInitialBackoff time.Duration `yaml:"reconnect_initial_backoff" json:"reconnect_initial_backoff"`
	ReconnectMaxBackoff     time.Duration `yaml:"reconnect_max_backoff" json:"reconnect_max_backoff"`
}

// LoadServer reads a server-only config file. Use .json for JSON; otherwise YAML is assumed.
func LoadServer(path string) (*Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if isJSONPath(path) {
		return ParseServerJSON(data)
	}
	return ParseServerYAML(data)
}

// ParseServerYAML parses server config from YAML bytes.
func ParseServerYAML(data []byte) (*Server, error) {
	var s Server
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return validateServer(&s)
}

// ParseServerJSON parses server config from JSON bytes (e.g. future admin API).
// Duration fields may be strings like "30s" or int64 nanoseconds.
func ParseServerJSON(data []byte) (*Server, error) {
	var wire struct {
		SocksListen             string          `json:"socks_listen"`
		DeviceListen            string          `json:"device_listen"`
		TLSCertFile             string          `json:"tls_cert_file"`
		TLSKeyFile              string          `json:"tls_key_file"`
		SocksAuthPassword       string          `json:"socks_auth_password"`
		SessionHeartbeatTimeout json.RawMessage `json:"session_heartbeat_timeout"`
		DeviceWaitTimeout       json.RawMessage `json:"device_wait_timeout"`
		ConnectResultTimeout    json.RawMessage `json:"connect_result_timeout"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	s := &Server{
		SocksListen:       wire.SocksListen,
		DeviceListen:      wire.DeviceListen,
		TLSCertFile:       wire.TLSCertFile,
		TLSKeyFile:        wire.TLSKeyFile,
		SocksAuthPassword: wire.SocksAuthPassword,
	}
	var err error
	if s.SessionHeartbeatTimeout, err = parseJSONDurationField(wire.SessionHeartbeatTimeout, "session_heartbeat_timeout"); err != nil {
		return nil, err
	}
	if s.DeviceWaitTimeout, err = parseJSONDurationField(wire.DeviceWaitTimeout, "device_wait_timeout"); err != nil {
		return nil, err
	}
	if s.ConnectResultTimeout, err = parseJSONDurationField(wire.ConnectResultTimeout, "connect_result_timeout"); err != nil {
		return nil, err
	}
	return validateServer(s)
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

func validateServer(s *Server) (*Server, error) {
	if s.SocksListen == "" {
		return nil, fmt.Errorf("socks_listen is required")
	}
	if s.DeviceListen == "" {
		return nil, fmt.Errorf("device_listen is required")
	}
	if s.TLSCertFile == "" || s.TLSKeyFile == "" {
		return nil, fmt.Errorf("tls_cert_file and tls_key_file are required")
	}
	if s.ConnectResultTimeout == 0 {
		s.ConnectResultTimeout = 30 * time.Second
	}
	return s, nil
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

func isJSONPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
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
		Token                   string          `json:"token"`
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
		Token:      wire.Token,
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
