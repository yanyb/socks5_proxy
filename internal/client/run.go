package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"my_socks5_proxy/internal/config"
	"my_socks5_proxy/internal/protocol"
)

// Run connects to the server with automatic reconnect and exponential backoff on failures or disconnects.
func Run(ctx context.Context, cfg *config.Client, logger *log.Logger) error {
	tlsCfg := clientTLSConfig(cfg)
	rb := newReconnectBackoff(cfg.ReconnectInitialBackoff, cfg.ReconnectMaxBackoff)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := runSession(ctx, cfg, tlsCfg, logger, rb)
		if errors.Is(err, context.Canceled) {
			return err
		}
		if err != nil {
			logger.Printf("client: session ended: %v", err)
		}

		delay := rb.Next()
		logger.Printf("client: reconnect in %v", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// runSession holds one TLS+yamux session until it breaks or ctx is cancelled.
func runSession(ctx context.Context, cfg *config.Client, tlsCfg *tls.Config, logger *log.Logger, rb *reconnectBackoff) error {
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("dial server: %w", err)
	}
	tlsConn := tls.Client(conn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("tls handshake: %w", err)
	}

	sess, err := yamux.Client(tlsConn, yamuxConfig())
	if err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("yamux client: %w", err)
	}
	defer sess.Close()

	ctrl, err := sess.OpenStream()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	br := bufio.NewReader(ctrl)

	if err := protocol.WriteLine(ctrl, &protocol.Envelope{
		Type:     protocol.TypeRegister,
		DeviceID: cfg.DeviceID,
		Token:    cfg.Token,
	}); err != nil {
		return err
	}
	ack, err := protocol.ReadLine(br)
	if err != nil {
		return fmt.Errorf("register ack: %w", err)
	}
	if ack.Type != protocol.TypeRegisterAck || !ack.OK {
		msg := ack.Message
		if msg == "" {
			msg = "register rejected"
		}
		return fmt.Errorf("register: %s", msg)
	}

	rb.Reset()
	logger.Printf("client: registered as %s", cfg.DeviceID)

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		err := heartbeatLoop(sessCtx, ctrl, br, cfg.HeartbeatInterval, logger)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Printf("heartbeat exit: %v", err)
		}
		cancel()
		_ = sess.Close()
	}()

	err = acceptLoop(sessCtx, sess, logger)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 30 * time.Second
	return c
}

// clientTLSConfig builds TLS for the device→server link: encrypted, but no certificate verification (trust server).
// If server_addr uses a hostname (not an IP), sets ServerName for SNI.
func clientTLSConfig(cfg *config.Client) *tls.Config {
	tc := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}
	host, _, err := net.SplitHostPort(cfg.ServerAddr)
	if err != nil {
		return tc
	}
	if net.ParseIP(host) == nil {
		tc.ServerName = host
	}
	return tc
}

func heartbeatLoop(ctx context.Context, ctrl net.Conn, br *bufio.Reader, every time.Duration, logger *log.Logger) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			ts := time.Now().Unix()
			if err := protocol.WriteLine(ctrl, &protocol.Envelope{Type: protocol.TypeHeartbeat, Ts: ts}); err != nil {
				return err
			}
			env, err := protocol.ReadLine(br)
			if err != nil {
				return err
			}
			if env.Type != protocol.TypeHeartbeatAck {
				logger.Printf("client: unexpected control message: %q", env.Type)
			}
		}
	}
}

func acceptLoop(ctx context.Context, sess *yamux.Session, logger *log.Logger) error {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go handleConnectStream(stream, logger)
	}
}

func handleConnectStream(stream net.Conn, logger *log.Logger) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	env, err := protocol.ReadLine(br)
	if err != nil {
		logger.Printf("connect: read request: %v", err)
		return
	}
	if env.Type != protocol.TypeConnect || env.ID == "" || env.Address == "" {
		_ = protocol.WriteLine(stream, &protocol.Envelope{
			Type:    protocol.TypeConnectResult,
			ID:      env.ID,
			OK:      false,
			Message: "bad connect request",
		})
		return
	}
	network := env.Network
	if network == "" {
		network = "tcp"
	}
	d := net.Dialer{Timeout: 30 * time.Second}
	target, err := d.Dial(network, env.Address)
	if err != nil {
		_ = protocol.WriteLine(stream, &protocol.Envelope{
			Type:    protocol.TypeConnectResult,
			ID:      env.ID,
			OK:      false,
			Message: err.Error(),
		})
		return
	}

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type: protocol.TypeConnectResult,
		ID:   env.ID,
		OK:   true,
	}); err != nil {
		_ = target.Close()
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(target, stream)
		_ = target.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stream, target)
	}()
	wg.Wait()
}
