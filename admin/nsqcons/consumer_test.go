package nsqcons

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"xsocks5/admin/store"
	"xsocks5/protocol/heartbeat"

	"github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
)

// recorderStore captures the last UpdateDeviceNetwork call and can be told
// what to return.
type recorderStore struct {
	mu      sync.Mutex
	gotID   string
	gotSnap store.NetworkSnapshot
	calls   int
	ret     error
}

func (r *recorderStore) UpsertDevice(context.Context, store.DeviceInfo) (*store.Device, error) {
	return nil, nil
}
func (r *recorderStore) UpdateDeviceNetwork(_ context.Context, id string, snap store.NetworkSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.gotID = id
	r.gotSnap = snap
	return r.ret
}
func (r *recorderStore) Close(context.Context) error { return nil }

// newConsumerForTest builds a Consumer wired to rec but never connects to NSQ.
// We exercise handle() directly with synthetic nsq.Message bodies.
func newConsumerForTest(t *testing.T, rec store.DeviceStore) *Consumer {
	t.Helper()
	c, err := New(Config{
		Topic:           "device.heartbeat",
		Channel:         "test",
		LookupdHTTPAddr: []string{"127.0.0.1:4161"}, // not actually used here
		Concurrency:     1,
	}, rec, logrus.New())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func msg(t *testing.T, evt heartbeat.Event) *nsq.Message {
	t.Helper()
	body, _ := json.Marshal(evt)
	return nsq.NewMessage(nsq.MessageID{}, body)
}

func TestHandle_OK(t *testing.T) {
	rec := &recorderStore{}
	c := newConsumerForTest(t, rec)
	avg := int64(45)
	loss := 0.1
	err := c.handle(msg(t, heartbeat.Event{
		DeviceID:     "dev-1",
		RemoteIP:     "1.2.3.4",
		NetType:      "5g",
		ServerRecvMs: 1700000000000,
		AvgRTTms:     &avg,
		LossRate:     &loss,
		Geo:          heartbeat.Geo{Country: "US", Region: "California"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if rec.gotID != "dev-1" || rec.gotSnap.LastIP != "1.2.3.4" || rec.gotSnap.Country != "US" {
		t.Fatalf("unexpected snap: id=%q snap=%+v", rec.gotID, rec.gotSnap)
	}
	if rec.gotSnap.AvgRTTms == nil || *rec.gotSnap.AvgRTTms != 45 {
		t.Fatalf("avg_rtt not propagated: %+v", rec.gotSnap.AvgRTTms)
	}
}

func TestHandle_BadJSON_Drops(t *testing.T) {
	rec := &recorderStore{}
	c := newConsumerForTest(t, rec)
	bad := nsq.NewMessage(nsq.MessageID{}, []byte("not-json"))
	if err := c.handle(bad); err != nil {
		t.Fatalf("bad json should be dropped (no error), got %v", err)
	}
	if rec.calls != 0 {
		t.Fatal("store should not be called for bad json")
	}
}

func TestHandle_EmptyDeviceID_Drops(t *testing.T) {
	rec := &recorderStore{}
	c := newConsumerForTest(t, rec)
	if err := c.handle(msg(t, heartbeat.Event{ServerRecvMs: 1})); err != nil {
		t.Fatalf("empty device_id should be dropped, got %v", err)
	}
	if rec.calls != 0 {
		t.Fatal("store should not be called for empty device_id")
	}
}

func TestHandle_UnknownDevice_Drops(t *testing.T) {
	rec := &recorderStore{ret: store.ErrUnknownDevice}
	c := newConsumerForTest(t, rec)
	if err := c.handle(msg(t, heartbeat.Event{DeviceID: "ghost"})); err != nil {
		t.Fatalf("unknown device should be soft-dropped, got %v", err)
	}
}

func TestHandle_StoreError_Requeues(t *testing.T) {
	rec := &recorderStore{ret: errors.New("mongo down")}
	c := newConsumerForTest(t, rec)
	if err := c.handle(msg(t, heartbeat.Event{DeviceID: "dev-1"})); err == nil {
		t.Fatal("store error must be returned so NSQ requeues")
	}
}
