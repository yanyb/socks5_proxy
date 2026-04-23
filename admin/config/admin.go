// Package config holds admin-service settings parsed from YAML/JSON.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Admin is the admin-service config: HTTP listen, AES key (for /register
// payload decryption), Mongo storage, and the runtime config returned to
// devices on successful registration.
type Admin struct {
	// HTTPListen e.g. ":8080" or "127.0.0.1:8080".
	HTTPListen string `yaml:"http_listen" json:"http_listen"`

	// AESKey is the symmetric key used to decrypt /register playloads. Provide
	// exactly one of AESKeyB64 / AESKeyHex / AESKeyText. 16/24/32 raw bytes
	// pick AES-128/192/256 directly; any other length is SHA-256 hashed (AES-256).
	AESKeyB64  string `yaml:"aes_key_b64" json:"aes_key_b64"`
	AESKeyHex  string `yaml:"aes_key_hex" json:"aes_key_hex"`
	AESKeyText string `yaml:"aes_key_text" json:"aes_key_text"`

	// MongoURI e.g. "mongodb://localhost:27017".
	MongoURI string `yaml:"mongo_uri" json:"mongo_uri"`
	// MongoDB defaults to "xsocks5".
	MongoDB string `yaml:"mongo_db" json:"mongo_db"`
	// DeviceCollection defaults to "devices".
	DeviceCollection string `yaml:"device_collection" json:"device_collection"`

	// Returned to devices on /register success. Seconds.
	ServerAddr                 string `yaml:"server_addr" json:"server_addr"`
	HeartbeatIntervalSec       int    `yaml:"heartbeat_interval" json:"heartbeat_interval"`
	ReconnectInitialBackoffSec int    `yaml:"reconnect_initial_backoff" json:"reconnect_initial_backoff"`
	ReconnectMaxBackoffSec     int    `yaml:"reconnect_max_backoff" json:"reconnect_max_backoff"`

	// MaxClockSkewSec rejects /register when |server_now - device_timestamp|
	// exceeds this many seconds (prevents replay of stale envelopes). Default 300.
	MaxClockSkewSec int `yaml:"max_clock_skew_sec" json:"max_clock_skew_sec"`

	// HTTP timeouts for the admin server. ms.
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout" json:"write_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`

	// Logging (logrus). level: trace|debug|info|warn|error. format: text|json.
	LogLevel  string `yaml:"log_level" json:"log_level"`
	LogFormat string `yaml:"log_format" json:"log_format"`
	// "" or "stdout" -> stdout; "stderr" -> stderr; else file path with lumberjack rotation.
	LogFile string `yaml:"log_file" json:"log_file"`

	// NSQ heartbeat consumer. Empty NSQLookupdHTTPAddrs disables consumption
	// (admin then runs only as the /register HTTP service).
	NSQLookupdHTTPAddrs []string `yaml:"nsq_lookupd_http_addrs" json:"nsq_lookupd_http_addrs"`
	// HeartbeatTopic defaults to "device.heartbeat" (matches server side).
	HeartbeatTopic string `yaml:"heartbeat_topic" json:"heartbeat_topic"`
	// HeartbeatChannel: each channel gets its own copy of every message. Use
	// "admin-mongo" here; future consumers (dashboard, alerts) should pick
	// their own channel name.
	HeartbeatChannel string `yaml:"heartbeat_channel" json:"heartbeat_channel"`
	// HeartbeatConcurrency: max in-flight messages and parallel handler
	// goroutines for the consumer. 8 is fine for Mongo writes.
	HeartbeatConcurrency int `yaml:"heartbeat_concurrency" json:"heartbeat_concurrency"`
}

// AESKeyBytes resolves the configured key into raw bytes. The first non-empty
// of AESKeyB64 / AESKeyHex / AESKeyText wins. Returned bytes may have any
// length; aescbc.NormalizeKey makes it a valid AES key.
func (a *Admin) AESKeyBytes() ([]byte, error) {
	if s := strings.TrimSpace(a.AESKeyB64); s != "" {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("aes_key_b64: %w", err)
		}
		return b, nil
	}
	if s := strings.TrimSpace(a.AESKeyHex); s != "" {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("aes_key_hex: %w", err)
		}
		return b, nil
	}
	if s := a.AESKeyText; s != "" {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("admin: aes_key_b64/aes_key_hex/aes_key_text is required")
}

// LoadAdmin reads YAML (default) or JSON (.json suffix) from path.
func LoadAdmin(path string) (*Admin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return ParseAdminJSON(data)
	}
	return ParseAdminYAML(data)
}

// ParseAdminYAML parses YAML bytes.
func ParseAdminYAML(data []byte) (*Admin, error) {
	var a Admin
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return validate(&a)
}

// ParseAdminJSON parses JSON bytes. Duration fields accept "30s" or int64 ns.
func ParseAdminJSON(data []byte) (*Admin, error) {
	var wire struct {
		HTTPListen                 string          `json:"http_listen"`
		AESKeyB64                  string          `json:"aes_key_b64"`
		AESKeyHex                  string          `json:"aes_key_hex"`
		AESKeyText                 string          `json:"aes_key_text"`
		MongoURI                   string          `json:"mongo_uri"`
		MongoDB                    string          `json:"mongo_db"`
		DeviceCollection           string          `json:"device_collection"`
		ServerAddr                 string          `json:"server_addr"`
		HeartbeatIntervalSec       int             `json:"heartbeat_interval"`
		ReconnectInitialBackoffSec int             `json:"reconnect_initial_backoff"`
		ReconnectMaxBackoffSec     int             `json:"reconnect_max_backoff"`
		MaxClockSkewSec            int             `json:"max_clock_skew_sec"`
		ReadHeaderTimeout          json.RawMessage `json:"read_header_timeout"`
		ReadTimeout                json.RawMessage `json:"read_timeout"`
		WriteTimeout               json.RawMessage `json:"write_timeout"`
		ShutdownTimeout            json.RawMessage `json:"shutdown_timeout"`
		LogLevel                   string          `json:"log_level"`
		LogFormat                  string          `json:"log_format"`
		LogFile                    string          `json:"log_file"`
		NSQLookupdHTTPAddrs        []string        `json:"nsq_lookupd_http_addrs"`
		HeartbeatTopic             string          `json:"heartbeat_topic"`
		HeartbeatChannel           string          `json:"heartbeat_channel"`
		HeartbeatConcurrency       int             `json:"heartbeat_concurrency"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	a := &Admin{
		HTTPListen:                 wire.HTTPListen,
		AESKeyB64:                  wire.AESKeyB64,
		AESKeyHex:                  wire.AESKeyHex,
		AESKeyText:                 wire.AESKeyText,
		MongoURI:                   wire.MongoURI,
		MongoDB:                    wire.MongoDB,
		DeviceCollection:           wire.DeviceCollection,
		ServerAddr:                 wire.ServerAddr,
		HeartbeatIntervalSec:       wire.HeartbeatIntervalSec,
		ReconnectInitialBackoffSec: wire.ReconnectInitialBackoffSec,
		ReconnectMaxBackoffSec:     wire.ReconnectMaxBackoffSec,
		MaxClockSkewSec:            wire.MaxClockSkewSec,
		LogLevel:                   wire.LogLevel,
		LogFormat:                  wire.LogFormat,
		LogFile:                    wire.LogFile,
		NSQLookupdHTTPAddrs:        wire.NSQLookupdHTTPAddrs,
		HeartbeatTopic:             wire.HeartbeatTopic,
		HeartbeatChannel:           wire.HeartbeatChannel,
		HeartbeatConcurrency:       wire.HeartbeatConcurrency,
	}
	var err error
	if a.ReadHeaderTimeout, err = parseDur(wire.ReadHeaderTimeout, "read_header_timeout"); err != nil {
		return nil, err
	}
	if a.ReadTimeout, err = parseDur(wire.ReadTimeout, "read_timeout"); err != nil {
		return nil, err
	}
	if a.WriteTimeout, err = parseDur(wire.WriteTimeout, "write_timeout"); err != nil {
		return nil, err
	}
	if a.ShutdownTimeout, err = parseDur(wire.ShutdownTimeout, "shutdown_timeout"); err != nil {
		return nil, err
	}
	return validate(a)
}

func validate(a *Admin) (*Admin, error) {
	if strings.TrimSpace(a.HTTPListen) == "" {
		return nil, fmt.Errorf("http_listen is required")
	}
	if strings.TrimSpace(a.MongoURI) == "" {
		return nil, fmt.Errorf("mongo_uri is required")
	}
	if strings.TrimSpace(a.ServerAddr) == "" {
		return nil, fmt.Errorf("server_addr is required (returned to devices)")
	}
	if _, err := a.AESKeyBytes(); err != nil {
		return nil, err
	}
	if a.MongoDB == "" {
		a.MongoDB = "xsocks5"
	}
	if a.DeviceCollection == "" {
		a.DeviceCollection = "devices"
	}
	if a.HeartbeatIntervalSec <= 0 {
		a.HeartbeatIntervalSec = 30
	}
	if a.ReconnectInitialBackoffSec <= 0 {
		a.ReconnectInitialBackoffSec = 1
	}
	if a.ReconnectMaxBackoffSec <= 0 {
		a.ReconnectMaxBackoffSec = 60
	}
	if a.MaxClockSkewSec <= 0 {
		a.MaxClockSkewSec = 300
	}
	if a.ReadHeaderTimeout <= 0 {
		a.ReadHeaderTimeout = 5 * time.Second
	}
	if a.ReadTimeout <= 0 {
		a.ReadTimeout = 15 * time.Second
	}
	if a.WriteTimeout <= 0 {
		a.WriteTimeout = 15 * time.Second
	}
	if a.ShutdownTimeout <= 0 {
		a.ShutdownTimeout = 10 * time.Second
	}
	return a, nil
}

func parseDur(raw json.RawMessage, name string) (time.Duration, error) {
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
		return 0, fmt.Errorf("%s: expected duration string or int64 ns", name)
	}
	return time.Duration(ns), nil
}
