// Package handler holds the admin HTTP handlers.
//
// /register flow:
//  1. Decode JSON envelope B: { "playload": base64(IV(16)||AES_CBC_PKCS7(...)),
//     "timestamp": <ms> }
//  2. base64-decode playload, AES-CBC decrypt, PKCS7 unpad -> base64 string.
//  3. base64-decode that -> JSON A bytes -> DeviceInfo.
//  4. Reject if A.timestamp != envelope.timestamp.
//  5. Reject if |server_now_ms - timestamp| > MaxClockSkew.
//  6. Upsert into the device store.
//  7. Respond JSON { device_id, server_addr, heartbeat_interval, ... }.
package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"xsocks5/admin/config"
	"xsocks5/admin/store"
	"xsocks5/common/crypto/aescbc"

	"github.com/sirupsen/logrus"
)

// requestEnvelope mirrors mobile/client side; field name kept literal ("playload").
type requestEnvelope struct {
	Playload  string `json:"playload"`
	Timestamp int64  `json:"timestamp"`
}

type devicePayload struct {
	OS           string `json:"os"`
	Brand        string `json:"brand"`
	Model        string `json:"model"`
	IMEI         string `json:"imei"`
	AndroidID    string `json:"android_id"`
	SerialNumber string `json:"serial_number"`
	Timestamp    int64  `json:"timestamp"`
}

type response struct {
	DeviceID                   string `json:"device_id"`
	ServerAddr                 string `json:"server_addr"`
	HeartbeatIntervalSec       int    `json:"heartbeat_interval"`
	ReconnectInitialBackoffSec int    `json:"reconnect_initial_backoff"`
	ReconnectMaxBackoffSec     int    `json:"reconnect_max_backoff"`
}

const maxBodyBytes = 64 * 1024

// RegisterHandler is HTTP handler for POST /register.
type RegisterHandler struct {
	cfg    *config.Admin
	aesKey []byte
	store  store.DeviceStore
	log    *logrus.Logger
}

// New constructs a RegisterHandler. cfg.AESKeyBytes() must succeed.
func New(cfg *config.Admin, st store.DeviceStore, log *logrus.Logger) (*RegisterHandler, error) {
	key, err := cfg.AESKeyBytes()
	if err != nil {
		return nil, err
	}
	return &RegisterHandler{cfg: cfg, aesKey: key, store: st, log: log}, nil
}

// ServeHTTP implements http.Handler.
func (h *RegisterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var env requestEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json envelope")
		return
	}
	if env.Playload == "" || env.Timestamp == 0 {
		writeError(w, http.StatusBadRequest, "missing playload or timestamp")
		return
	}
	plain, err := h.decryptPlayload(env.Playload)
	if err != nil {
		h.log.WithError(err).WithField("remote", clientIP(r)).Warn("register: decrypt failed")
		writeError(w, http.StatusBadRequest, "decrypt failed")
		return
	}
	var dp devicePayload
	if err := json.Unmarshal(plain, &dp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid inner json")
		return
	}
	if dp.Timestamp != env.Timestamp {
		writeError(w, http.StatusBadRequest, "timestamp mismatch")
		return
	}
	skewMs := int64(h.cfg.MaxClockSkewSec) * 1000
	if delta := abs64(time.Now().UnixMilli() - env.Timestamp); delta > skewMs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("clock skew too large: %dms", delta))
		return
	}
	dev, err := h.store.UpsertDevice(r.Context(), store.DeviceInfo{
		OS:                dp.OS,
		Brand:             dp.Brand,
		Model:             dp.Model,
		IMEI:              dp.IMEI,
		AndroidID:         dp.AndroidID,
		SerialNumber:      dp.SerialNumber,
		ClientTimestampMs: dp.Timestamp,
		RemoteAddr:        clientIP(r),
	})
	if err != nil {
		h.log.WithError(err).Error("register: upsert failed")
		writeError(w, http.StatusInternalServerError, "store error")
		return
	}
	h.log.WithFields(logrus.Fields{
		"device_id":      dev.DeviceID,
		"register_count": dev.RegisterCount,
		"remote":         clientIP(r),
		"brand":          dev.Brand,
		"model":          dev.Model,
	}).Info("device registered")
	writeJSON(w, http.StatusOK, response{
		DeviceID:                   dev.DeviceID,
		ServerAddr:                 h.cfg.ServerAddr,
		HeartbeatIntervalSec:       h.cfg.HeartbeatIntervalSec,
		ReconnectInitialBackoffSec: h.cfg.ReconnectInitialBackoffSec,
		ReconnectMaxBackoffSec:     h.cfg.ReconnectMaxBackoffSec,
	})
}

// decryptPlayload follows the spec:
//
//	playload(b64) -> wire(IV||ct) -> AES-CBC decrypt -> base64 string -> JSON A bytes
func (h *RegisterHandler) decryptPlayload(b64 string) ([]byte, error) {
	wire, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("playload base64: %w", err)
	}
	plain, err := aescbc.Decrypt(h.aesKey, wire)
	if err != nil {
		return nil, fmt.Errorf("aes-cbc: %w", err)
	}
	jsonA, err := base64.StdEncoding.DecodeString(string(plain))
	if err != nil {
		return nil, fmt.Errorf("inner base64: %w", err)
	}
	return jsonA, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clientIP picks X-Forwarded-For (first hop) when present, else RemoteAddr.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
