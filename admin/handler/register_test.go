package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"xsocks5/admin/config"
	"xsocks5/admin/store"
	"xsocks5/client/register"

	"github.com/sirupsen/logrus"
)

type fakeStore struct {
	last store.DeviceInfo
	out  *store.Device
}

func (f *fakeStore) UpsertDevice(_ context.Context, info store.DeviceInfo) (*store.Device, error) {
	f.last = info
	return f.out, nil
}
func (f *fakeStore) UpdateDeviceNetwork(context.Context, string, store.NetworkSnapshot) error {
	return nil
}
func (f *fakeStore) Close(_ context.Context) error { return nil }

func newAdminCfg(key string) *config.Admin {
	return &config.Admin{
		HTTPListen:                 ":0",
		AESKeyText:                 key,
		MongoURI:                   "mongodb://test",
		ServerAddr:                 "127.0.0.1:8888",
		HeartbeatIntervalSec:       10,
		ReconnectInitialBackoffSec: 1,
		ReconnectMaxBackoffSec:     60,
		MaxClockSkewSec:            300,
	}
}

func encodeBody(t *testing.T, key string, info map[string]any, ts int64) []byte {
	t.Helper()
	jsonA, _ := json.Marshal(info)
	playload, err := register.BuildRegisterPlayloadB64([]byte(key), jsonA)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"playload":  playload,
		"timestamp": ts,
	})
	return body
}

func TestRegister_OK(t *testing.T) {
	const key = "shared-with-app-key"
	cfg := newAdminCfg(key)
	st := &fakeStore{out: &store.Device{
		DeviceID: "dev-abc123", Brand: "xiaomi", Model: "test",
		FirstRegisterAt: time.Now(), LastRegisterAt: time.Now(),
		RegisterCount: 1,
	}}
	h, err := New(cfg, st, logrus.New())
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UnixMilli()
	body := encodeBody(t, key, map[string]any{
		"os":            "android",
		"brand":         "xiaomi",
		"model":         "mi-test",
		"imei":          "123",
		"android_id":    "abc",
		"serial_number": "",
		"timestamp":     ts,
	}, ts)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.RemoteAddr = "1.2.3.4:55555"
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		raw, _ := io.ReadAll(rr.Body)
		t.Fatalf("status=%d body=%s", rr.Code, raw)
	}
	var out response
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.DeviceID != "dev-abc123" || out.ServerAddr != "127.0.0.1:8888" || out.HeartbeatIntervalSec != 10 {
		t.Fatalf("bad response: %+v", out)
	}
	if st.last.AndroidID != "abc" || st.last.Brand != "xiaomi" || st.last.RemoteAddr != "1.2.3.4" {
		t.Fatalf("bad upsert input: %+v", st.last)
	}
}

func TestRegister_TimestampMismatch(t *testing.T) {
	const key = "k"
	cfg := newAdminCfg(key)
	h, _ := New(cfg, &fakeStore{out: &store.Device{}}, logrus.New())
	ts := time.Now().UnixMilli()
	body := encodeBody(t, key, map[string]any{"os": "android", "timestamp": ts}, ts+1)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestRegister_ClockSkewTooLarge(t *testing.T) {
	const key = "k"
	cfg := newAdminCfg(key)
	cfg.MaxClockSkewSec = 1
	h, _ := New(cfg, &fakeStore{out: &store.Device{}}, logrus.New())
	old := time.Now().Add(-1 * time.Hour).UnixMilli()
	body := encodeBody(t, key, map[string]any{"os": "android", "timestamp": old}, old)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestRegister_BadDecrypt(t *testing.T) {
	cfg := newAdminCfg("real-key")
	h, _ := New(cfg, &fakeStore{out: &store.Device{}}, logrus.New())
	ts := time.Now().UnixMilli()
	body := encodeBody(t, "wrong-key", map[string]any{"os": "android", "timestamp": ts}, ts)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestRegister_OnlyPOST(t *testing.T) {
	cfg := newAdminCfg("k")
	h, _ := New(cfg, &fakeStore{out: &store.Device{}}, logrus.New())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rr.Code)
	}
}
