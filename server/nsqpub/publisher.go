// Package nsqpub publishes per-heartbeat events to NSQ. The producer connects
// directly to a single nsqd (typically the local one); consumers discover via
// nsqlookupd. This is the standard NSQ topology.
package nsqpub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"xsocks5/protocol/heartbeat"

	"github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
)

// Publisher is the seam used by the rest of the server (mainly the heartbeat
// sink). Real impl wraps an nsq.Producer; tests can substitute a recorder.
type Publisher interface {
	Publish(ctx context.Context, evt heartbeat.Event) error
	io.Closer
}

// NopPublisher discards events. Used when NSQ is not configured (early dev).
type NopPublisher struct{}

// Publish on NopPublisher is a no-op.
func (NopPublisher) Publish(context.Context, heartbeat.Event) error { return nil }

// Close on NopPublisher is a no-op.
func (NopPublisher) Close() error { return nil }

// NSQPublisher publishes JSON-encoded heartbeat events to a fixed topic.
type NSQPublisher struct {
	topic string
	prod  *nsq.Producer
	log   *logrus.Logger

	closed sync.Once
}

// nsqLogShim adapts logrus to nsq.Logger (which expects log.Logger semantics
// via Output(int, string)). All NSQ messages get tagged stream=nsq.
type nsqLogShim struct{ l *logrus.Logger }

func (s nsqLogShim) Output(_ int, msg string) error {
	s.l.WithField("nsq", true).Debug(msg)
	return nil
}

// New connects to nsqd at addr and prepares a publisher for topic.
//
// Typical addr: "127.0.0.1:4150" (local nsqd). The producer pings nsqd to
// validate connectivity; failure is fatal at boot rather than silently lost.
func New(addr, topic string, log *logrus.Logger) (*NSQPublisher, error) {
	if addr == "" {
		return nil, fmt.Errorf("nsqpub: nsqd address is empty")
	}
	if topic == "" {
		topic = heartbeat.Topic
	}
	cfg := nsq.NewConfig()
	cfg.DialTimeout = 5 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	prod, err := nsq.NewProducer(addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("nsqpub: new producer: %w", err)
	}
	prod.SetLogger(nsqLogShim{l: log}, nsq.LogLevelInfo)
	if err := prod.Ping(); err != nil {
		prod.Stop()
		return nil, fmt.Errorf("nsqpub: ping %s: %w", addr, err)
	}
	return &NSQPublisher{topic: topic, prod: prod, log: log}, nil
}

// Publish marshals evt and ships it to NSQ. ctx is honored only for the
// JSON encode path; the actual TCP publish has its own WriteTimeout.
func (p *NSQPublisher) Publish(ctx context.Context, evt heartbeat.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("nsqpub: marshal: %w", err)
	}
	if err := p.prod.Publish(p.topic, body); err != nil {
		return fmt.Errorf("nsqpub: publish: %w", err)
	}
	return nil
}

// Close stops the producer. Idempotent.
func (p *NSQPublisher) Close() error {
	p.closed.Do(func() {
		p.prod.Stop()
	})
	return nil
}
