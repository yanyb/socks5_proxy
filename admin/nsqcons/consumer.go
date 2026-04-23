// Package nsqcons subscribes to the heartbeat NSQ topic and writes each
// event into the device store.
//
// Discovery uses nsqlookupd: pass the lookupd HTTP addrs (e.g.
// "127.0.0.1:4161"). We do NOT connect directly to nsqd; lookupd lets us
// scale producers/consumers without any reconfig.
package nsqcons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"xsocks5/admin/store"
	"xsocks5/protocol/heartbeat"

	"github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
)

// upsertTimeout bounds each Mongo write triggered by an NSQ message. Mongo
// outages should surface as requeue, not as goroutine pile-up.
const upsertTimeout = 5 * time.Second

// Consumer wraps an nsq.Consumer + the device store.
type Consumer struct {
	cons       *nsq.Consumer
	topic      string
	chann      string
	lookupd    []string
	concurrent int
	store      store.DeviceStore
	log        *logrus.Logger
	closed     sync.Once
}

// Config is what main passes in. Concurrency is the per-channel max in-flight
// + handler goroutine count; default 8 is fine for Mongo writes.
type Config struct {
	Topic           string
	Channel         string
	LookupdHTTPAddr []string
	Concurrency     int
	// MaxAttempts after which a poison message is dropped (logged as error).
	// 0 -> NSQ default (10). Set lower for fast give-up on bad payloads.
	MaxAttempts uint16
}

type nsqLogShim struct{ l *logrus.Logger }

func (s nsqLogShim) Output(_ int, msg string) error {
	s.l.WithField("nsq", true).Debug(msg)
	return nil
}

// New builds (but does not start) a Consumer.
func New(cfg Config, st store.DeviceStore, log *logrus.Logger) (*Consumer, error) {
	if cfg.Topic == "" {
		cfg.Topic = heartbeat.Topic
	}
	if cfg.Channel == "" {
		cfg.Channel = "admin-mongo"
	}
	if len(cfg.LookupdHTTPAddr) == 0 {
		return nil, errors.New("nsqcons: at least one nsq_lookupd_http_addr is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	nc := nsq.NewConfig()
	if cfg.MaxAttempts > 0 {
		nc.MaxAttempts = cfg.MaxAttempts
	}
	nc.MaxInFlight = cfg.Concurrency
	c, err := nsq.NewConsumer(cfg.Topic, cfg.Channel, nc)
	if err != nil {
		return nil, fmt.Errorf("nsqcons: new consumer: %w", err)
	}
	c.SetLogger(nsqLogShim{l: log}, nsq.LogLevelInfo)
	return &Consumer{
		cons:       c,
		topic:      cfg.Topic,
		chann:      cfg.Channel,
		lookupd:    append([]string(nil), cfg.LookupdHTTPAddr...),
		concurrent: cfg.Concurrency,
		store:      st,
		log:        log,
	}, nil
}

// Start registers the handler and connects to lookupd. Non-blocking; once
// returned, messages start flowing into the handler goroutines.
func (c *Consumer) Start() error {
	c.cons.AddConcurrentHandlers(nsq.HandlerFunc(c.handle), c.concurrent)
	if err := c.cons.ConnectToNSQLookupds(c.lookupd); err != nil {
		return fmt.Errorf("nsqcons: connect to lookupds %v: %w", c.lookupd, err)
	}
	c.log.WithFields(logrus.Fields{
		"topic":      c.topic,
		"channel":    c.chann,
		"lookupd":    c.lookupd,
		"concurrent": c.concurrent,
	}).Info("nsq consumer started")
	return nil
}

// Stop drains in-flight handlers and disconnects. Safe to call multiple times.
func (c *Consumer) Stop() {
	c.closed.Do(func() {
		c.cons.Stop()
		<-c.cons.StopChan
	})
}

// handle decodes one NSQ message and merges it into Mongo. Decisions:
//   - JSON decode error: ack (don't retry; bad payload won't fix itself).
//   - Empty device_id: ack (data error).
//   - ErrUnknownDevice: ack (heartbeat from a never-registered device, log warn).
//   - Any other store error (Mongo down): return err so NSQ requeues with backoff.
func (c *Consumer) handle(m *nsq.Message) error {
	var evt heartbeat.Event
	if err := json.Unmarshal(m.Body, &evt); err != nil {
		c.log.WithError(err).
			WithField("body_len", len(m.Body)).
			Warn("nsqcons: bad json, dropping")
		return nil
	}
	if evt.DeviceID == "" {
		c.log.Warn("nsqcons: empty device_id, dropping")
		return nil
	}
	snap := store.NetworkSnapshot{
		LastIP:       evt.RemoteIP,
		NetType:      evt.NetType,
		AvgRTTms:     evt.AvgRTTms,
		LossRate:     evt.LossRate,
		Country:      evt.Geo.Country,
		CountryName:  evt.Geo.CountryName,
		Region:       evt.Geo.Region,
		RegionCode:   evt.Geo.RegionCode,
		City:         evt.Geo.City,
		CurTsMs:      evt.CurTsMs,
		ServerRecvMs: evt.ServerRecvMs,
	}
	ctx, cancel := context.WithTimeout(context.Background(), upsertTimeout)
	defer cancel()
	err := c.store.UpdateDeviceNetwork(ctx, evt.DeviceID, snap)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrUnknownDevice):
		c.log.WithField("device_id", evt.DeviceID).
			Warn("nsqcons: heartbeat for unknown device, dropping")
		return nil
	default:
		c.log.WithError(err).
			WithField("device_id", evt.DeviceID).
			Error("nsqcons: update mongo failed (will requeue)")
		return err
	}
}
