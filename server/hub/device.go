package hub

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"time"
	"xsocks5/protocol"

	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"
)

// ServeDevice accepts one TLS+TCP connection from a device, runs yamux server, handles register + control stream.
func ServeDevice(
	raw net.Conn,
	reg *Registry,
	heartbeatTimeout time.Duration,
	logger *logrus.Logger,
) {
	defer raw.Close()

	remote := raw.RemoteAddr().String()
	connLog := logger.WithFields(logrus.Fields{
		"component":   "device",
		"remote_addr": remote,
	})

	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second

	sess, err := yamux.Server(raw, cfg)
	if err != nil {
		connLog.WithError(err).Error("yamux server")
		return
	}
	defer sess.Close()

	stream, err := sess.AcceptStream()
	if err != nil {
		connLog.WithError(err).Error("accept first stream")
		return
	}

	br := bufio.NewReader(stream)
	first, err := protocol.ReadLine(br)
	if err != nil {
		connLog.WithError(err).Error("read register")
		_ = stream.Close()
		return
	}
	if first.Type != protocol.TypeRegister || first.DeviceID == "" {
		_ = protocol.WriteLine(stream, &protocol.Envelope{
			Type:    protocol.TypeRegisterAck,
			OK:      false,
			Message: "expected register with device_id",
		})
		_ = stream.Close()
		return
	}

	devLog := connLog.WithField("device_id", first.DeviceID)

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type: protocol.TypeRegisterAck,
		OK:   true,
	}); err != nil {
		devLog.WithError(err).Error("write register_ack")
		_ = stream.Close()
		return
	}

	reg.Put(first.DeviceID, sess)
	devLog.Info("registered")

	defer reg.Remove(first.DeviceID, sess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	lastBeat := time.Now()

	if heartbeatTimeout > 0 {
		go func() {
			t := time.NewTicker(heartbeatTimeout / 3)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					mu.Lock()
					stale := time.Since(lastBeat) > heartbeatTimeout
					mu.Unlock()
					if stale {
						devLog.Warn("heartbeat timeout, closing")
						_ = raw.Close()
						cancel()
						return
					}
				}
			}
		}()
	}

	for {
		env, err := protocol.ReadLine(br)
		if err != nil {
			if err != io.EOF {
				devLog.WithError(err).Error("control read")
			} else {
				devLog.Debug("control stream closed")
			}
			return
		}
		switch env.Type {
		case protocol.TypeHeartbeat:
			mu.Lock()
			lastBeat = time.Now()
			mu.Unlock()
			devLog.WithField("ts", env.Ts).Debug("recv heartbeat")
			if err := protocol.WriteLine(stream, &protocol.Envelope{Type: protocol.TypeHeartbeatAck, Ts: env.Ts}); err != nil {
				devLog.WithError(err).Error("write heartbeat_ack")
			}
		default:
			devLog.WithField("type", env.Type).Debug("ignore unknown control frame")
		}
	}
}
