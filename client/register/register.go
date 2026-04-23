// Package register implements first-boot registration with the admin service:
//
//	POST https://api.nddtech.cn/register
//	body B = { "playload": base64(IV(16)||AES-CBC-PKCS7(base64(JSON A))), "timestamp": <ms> }
//
// The admin returns the runtime config used by the device client (server addr,
// device_id, heartbeat, reconnect backoff). The AES key is provisioned to the
// app at build time and passed to RegisterDevice; the URL is fixed.
package register

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AdminURL is the registration endpoint; same for all builds (per spec, hard-coded).
const AdminURL = "https://api.nddtech.cn/register"

// DeviceInfo is plaintext JSON A. All fields are best-effort: the app fills
// what the OS allows. timestamp is unix epoch in milliseconds and MUST equal
// the outer envelope timestamp.
type DeviceInfo struct {
	OS           string `json:"os"`
	Brand        string `json:"brand"`
	Model        string `json:"model"`
	IMEI         string `json:"imei,omitempty"`
	AndroidID    string `json:"android_id,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Timestamp    int64  `json:"timestamp"`
}

// requestEnvelope is JSON B sent to admin.
type requestEnvelope struct {
	// Playload is base64( IV(16) || AES-CBC-PKCS7(base64(JSON A)) ).
	// Field name kept literal per the integration spec ("playload", not "payload").
	Playload  string `json:"playload"`
	Timestamp int64  `json:"timestamp"`
}

// Response mirrors the admin's success body. Durations are in seconds.
type Response struct {
	DeviceID                   string `json:"device_id"`
	ServerAddr                 string `json:"server_addr"`
	HeartbeatIntervalSec       int    `json:"heartbeat_interval"`
	ReconnectInitialBackoffSec int    `json:"reconnect_initial_backoff"`
	ReconnectMaxBackoffSec     int    `json:"reconnect_max_backoff"`
}

// HeartbeatInterval / Reconnect* return the admin-provided seconds as time.Duration.
func (r *Response) HeartbeatInterval() time.Duration {
	return time.Duration(r.HeartbeatIntervalSec) * time.Second
}

// ReconnectInitialBackoff returns the admin-provided seconds as time.Duration.
func (r *Response) ReconnectInitialBackoff() time.Duration {
	return time.Duration(r.ReconnectInitialBackoffSec) * time.Second
}

// ReconnectMaxBackoff returns the admin-provided seconds as time.Duration.
func (r *Response) ReconnectMaxBackoff() time.Duration {
	return time.Duration(r.ReconnectMaxBackoffSec) * time.Second
}

// RegisterDevice posts the encrypted device info to admin and parses the response.
// httpClient is optional (default has 20s timeout).
func RegisterDevice(ctx context.Context, info DeviceInfo, aesKey []byte, httpClient *http.Client) (*Response, error) {
	if len(aesKey) == 0 {
		return nil, fmt.Errorf("register: aes key is required")
	}
	if info.Timestamp == 0 {
		info.Timestamp = time.Now().UnixMilli()
	}
	jsonA, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("register: marshal device info: %w", err)
	}
	playload, err := buildRegisterPlayloadB64(aesKey, jsonA)
	if err != nil {
		return nil, fmt.Errorf("register: encrypt: %w", err)
	}
	envBytes, err := json.Marshal(requestEnvelope{
		Playload:  playload,
		Timestamp: info.Timestamp,
	})
	if err != nil {
		return nil, fmt.Errorf("register: marshal envelope: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, AdminURL, bytes.NewReader(envBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("register: http: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("register: status %d: %s", res.StatusCode, snippet)
	}
	var out Response
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("register: parse response: %w", err)
	}
	if out.DeviceID == "" || out.ServerAddr == "" {
		return nil, fmt.Errorf("register: response missing device_id or server_addr")
	}
	return &out, nil
}
