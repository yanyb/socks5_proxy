// Package config holds server-only settings parsed from YAML/JSON.
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

// Server is cloud-side settings only. Unmarshaled from server/configs/server.yaml (or equivalent API payload).
type Server struct {
	SocksListen  string `yaml:"socks_listen" json:"socks_listen"`
	DeviceListen string `yaml:"device_listen" json:"device_listen"`
	TLSCertFile  string `yaml:"tls_cert_file" json:"tls_cert_file"`
	TLSKeyFile   string `yaml:"tls_key_file" json:"tls_key_file"`
	// SocksAuthPassword enables RFC SOCKS5 user/pass auth. Username must be the target device_id; password is this shared secret. Recommended when multiple phones are online.
	SocksAuthPassword       string        `yaml:"socks_auth_password" json:"socks_auth_password"`
	SessionHeartbeatTimeout time.Duration `yaml:"session_heartbeat_timeout" json:"session_heartbeat_timeout"`
	DeviceWaitTimeout       time.Duration `yaml:"device_wait_timeout" json:"device_wait_timeout"`
	ConnectResultTimeout    time.Duration `yaml:"connect_result_timeout" json:"connect_result_timeout"`
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
