package hub

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"
	"xsocks5/protocol"

	"github.com/hashicorp/yamux"
)

// ServeDevice accepts one TLS+TCP connection from a device, runs yamux server, handles register + control stream.
func ServeDevice(
	raw net.Conn,
	reg *Registry,
	heartbeatTimeout time.Duration,
	log *log.Logger,
) {
	defer raw.Close()

	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second

	sess, err := yamux.Server(raw, cfg)
	if err != nil {
		log.Printf("device: yamux server: %v", err)
		return
	}
	defer sess.Close()

	stream, err := sess.AcceptStream()
	if err != nil {
		log.Printf("device: accept first stream: %v", err)
		return
	}

	br := bufio.NewReader(stream)
	first, err := protocol.ReadLine(br)
	if err != nil {
		log.Printf("device: read register: %v", err)
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

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type: protocol.TypeRegisterAck,
		OK:   true,
	}); err != nil {
		log.Printf("device: write register_ack: %v", err)
		_ = stream.Close()
		return
	}

	reg.Put(first.DeviceID, sess)
	log.Printf("device: registered %s", first.DeviceID)

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
						log.Printf("device: heartbeat timeout, closing %s", first.DeviceID)
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
				log.Printf("device: control read %s: %v", first.DeviceID, err)
			}
			return
		}
		switch env.Type {
		case protocol.TypeHeartbeat:
			mu.Lock()
			lastBeat = time.Now()
			mu.Unlock()
			log.Println("device: recv heartbeat", first.DeviceID)
			err = protocol.WriteLine(stream, &protocol.Envelope{Type: protocol.TypeHeartbeatAck, Ts: env.Ts})
			if err != nil {
				log.Printf("device: write %s: %v", first.DeviceID, err)
			}
		default:
			// ignore unknown on control stream
		}
	}
}
