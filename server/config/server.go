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
	// SocksAuthPassword enables RFC SOCKS5 user/pass auth with a single shared secret.
	// Username must be the target device_id; password is this secret. Used as a fallback
	// when SocksCredentialsFile is empty.
	SocksAuthPassword string `yaml:"socks_auth_password" json:"socks_auth_password"`
	// SocksCredentialsFile is the JSON file holding {"username": "password", ...}
	// pairs. When set, it overrides SocksAuthPassword and is used to back a
	// refreshing credentials cache (see SocksCredentialsRefresh).
	// Future: replace this with an admin-service URL.
	SocksCredentialsFile string `yaml:"socks_credentials_file" json:"socks_credentials_file"`
	// SocksCredentialsRefresh is the cache reload period. Default 1m.
	SocksCredentialsRefresh time.Duration `yaml:"socks_credentials_refresh" json:"socks_credentials_refresh"`
	SessionHeartbeatTimeout time.Duration `yaml:"session_heartbeat_timeout" json:"session_heartbeat_timeout"`
	DeviceWaitTimeout       time.Duration `yaml:"device_wait_timeout" json:"device_wait_timeout"`
	ConnectResultTimeout    time.Duration `yaml:"connect_result_timeout" json:"connect_result_timeout"`

	// LogLevel is a logrus level: "trace", "debug", "info", "warn", "error". Default "info".
	LogLevel string `yaml:"log_level" json:"log_level"`
	// LogFormat is "text" (default) or "json".
	LogFormat string `yaml:"log_format" json:"log_format"`
	// DeviceLogFile routes the device-tunnel logger output. "" or "stdout" -> stdout,
	// "stderr" -> stderr, anything else -> file path (append).
	DeviceLogFile string `yaml:"device_log_file" json:"device_log_file"`
	// SocksLogFile routes the SOCKS5 logger output. Same semantics as DeviceLogFile.
	SocksLogFile string `yaml:"socks_log_file" json:"socks_log_file"`
	// ShutdownTimeout bounds how long graceful shutdown waits for in-flight goroutines
	// (device sessions + SOCKS connections) before forcing exit. Default 10s.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
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
		LogLevel                string          `json:"log_level"`
		LogFormat               string          `json:"log_format"`
		DeviceLogFile           string          `json:"device_log_file"`
		SocksLogFile            string          `json:"socks_log_file"`
		ShutdownTimeout         json.RawMessage `json:"shutdown_timeout"`
		SocksCredentialsFile    string          `json:"socks_credentials_file"`
		SocksCredentialsRefresh json.RawMessage `json:"socks_credentials_refresh"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	s := &Server{
		SocksListen:       wire.SocksListen,
		DeviceListen:      wire.DeviceListen,
		TLSCertFile:       wire.TLSCertFile,
		TLSKeyFile:        wire.TLSKeyFile,
		SocksAuthPassword:    wire.SocksAuthPassword,
		LogLevel:             wire.LogLevel,
		LogFormat:            wire.LogFormat,
		DeviceLogFile:        wire.DeviceLogFile,
		SocksLogFile:         wire.SocksLogFile,
		SocksCredentialsFile: wire.SocksCredentialsFile,
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
	if s.ShutdownTimeout, err = parseJSONDurationField(wire.ShutdownTimeout, "shutdown_timeout"); err != nil {
		return nil, err
	}
	if s.SocksCredentialsRefresh, err = parseJSONDurationField(wire.SocksCredentialsRefresh, "socks_credentials_refresh"); err != nil {
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
	if s.ShutdownTimeout == 0 {
		s.ShutdownTimeout = 10 * time.Second
	}
	if s.SocksCredentialsRefresh == 0 {
		s.SocksCredentialsRefresh = time.Minute
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
