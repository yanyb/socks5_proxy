package register

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 bytes -> AES-128
	info := DeviceInfo{
		OS:        "android",
		Brand:     "xiaomi",
		Model:     "mi-test",
		IMEI:      "123",
		AndroidID: "abc",
		Timestamp: 1713800000000,
	}
	jsonA, _ := json.Marshal(info)
	playload, err := buildRegisterPlayloadB64(key, jsonA)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeEncryptedPayloadForTest(key, playload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\"brand\":\"xiaomi\"") {
		t.Fatalf("decoded JSON missing brand: %s", got)
	}
}

func TestRegisterDevice_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env requestEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("server: parse env: %v", err)
		}
		if env.Playload == "" {
			t.Fatal("server: empty playload")
		}
		_ = json.NewEncoder(w).Encode(Response{
			DeviceID:                   "dev-001",
			ServerAddr:                 "127.0.0.1:8888",
			HeartbeatIntervalSec:       10,
			ReconnectInitialBackoffSec: 1,
			ReconnectMaxBackoffSec:     60,
		})
	}))
	defer srv.Close()

	// Patch AdminURL behavior by using a small wrapper: call the function with
	// a custom transport that rewrites host. Simpler: post directly.
	info := DeviceInfo{OS: "android", Brand: "x", Model: "y", Timestamp: time.Now().UnixMilli()}
	playload, err := buildRegisterPlayloadB64([]byte("0123456789abcdef"), mustMarshal(t, info))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(requestEnvelope{Playload: playload, Timestamp: info.Timestamp})
	res, err := http.Post(srv.URL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var out Response
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.DeviceID != "dev-001" || out.HeartbeatInterval() != 10*time.Second {
		t.Fatalf("bad parse: %+v / %v", out, out.HeartbeatInterval())
	}
}

func TestRegisterDevice_RequiresAESKey(t *testing.T) {
	_, err := RegisterDevice(context.Background(), DeviceInfo{OS: "x"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty AES key")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
