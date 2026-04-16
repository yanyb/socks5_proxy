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
	"strings"
	"sync"
	"time"

	"my_socks5_proxy/internal/config"
	"my_socks5_proxy/internal/protocol"

	"github.com/hashicorp/yamux"
)

// HostLookup resolves a hostname to IP address strings in Go (ordered dial attempts).
// If nil, the client uses Go's default resolver for the server dial and for SOCKS CONNECT targets.
//
// Android: gomobile cannot pass []string from Java; package mobile adapts HostResolver.LookupHost's
// newline-separated string via [ParseAddrsFromLookupString] before calling [RunWithHostLookup].
type HostLookup func(ctx context.Context, host string) ([]string, error)

// Run connects to the server with automatic reconnect and exponential backoff on failures or disconnects.
func Run(ctx context.Context, cfg *config.Client, logger *log.Logger) error {
	return runLoop(ctx, cfg, logger, nil)
}

// RunWithHostLookup is like Run but uses lookup for DNS when set: server hostname before the TLS dial,
// and SOCKS CONNECT target hostnames (so mobile can use Android/Java resolvers instead of Go's).
// TLS SNI to the server still uses the hostname from cfg.ServerAddr.
//
// The lookup callback always receives/returns []string here; the gomobile layer may use a string wire format.
func RunWithHostLookup(ctx context.Context, cfg *config.Client, logger *log.Logger, lookup HostLookup) error {
	return runLoop(ctx, cfg, logger, lookup)
}

// ParseAddrsFromLookupString splits newline-separated IP strings from an Android gomobile HostResolver.LookupHost result.
func ParseAddrsFromLookupString(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func runLoop(ctx context.Context, cfg *config.Client, logger *log.Logger, lookup HostLookup) error {
	tlsCfg := clientTLSConfig(cfg)
	rb := newReconnectBackoff(cfg.ReconnectInitialBackoff, cfg.ReconnectMaxBackoff)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := runSession(ctx, cfg, tlsCfg, logger, rb, lookup)
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
func runSession(ctx context.Context, cfg *config.Client, tlsCfg *tls.Config, logger *log.Logger, rb *reconnectBackoff, lookup HostLookup) error {
	conn, err := dialServerTCP(ctx, cfg, lookup)
	if err != nil {
		return err
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

	err = acceptLoop(sessCtx, sess, logger, lookup)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// dialServerTCP connects to cfg.ServerAddr. When lookup is set and the host is not a literal IP,
// lookup is used to obtain IP strings (Java/Android resolver); otherwise net.Dialer uses the default resolver.
func dialServerTCP(ctx context.Context, cfg *config.Client, lookup HostLookup) (net.Conn, error) {
	host, port, err := net.SplitHostPort(cfg.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("server_addr: %w", err)
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	if lookup == nil || net.ParseIP(host) != nil {
		conn, err := d.DialContext(ctx, "tcp", cfg.ServerAddr)
		if err != nil {
			return nil, fmt.Errorf("dial server: %w", err)
		}
		return conn, nil
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %q: no addresses", host)
	}
	var firstErr error
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		addr := net.JoinHostPort(ip.String(), port)
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		firstErr = err
	}
	if firstErr != nil {
		return nil, fmt.Errorf("dial server: %w", firstErr)
	}
	return nil, fmt.Errorf("dial server: no routable address from lookup")
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

func acceptLoop(ctx context.Context, sess *yamux.Session, logger *log.Logger, lookup HostLookup) error {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go handleConnectStream(ctx, stream, logger, lookup)
	}
}

func handleConnectStream(ctx context.Context, stream net.Conn, logger *log.Logger, lookup HostLookup) {
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
	logger.Printf("connecting to ", env.Address)
	network := env.Network
	if network == "" {
		network = "tcp"
	}
	target, err := dialConnectTarget(ctx, network, env.Address, lookup)
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

// dialConnectTarget dials env.Address for SOCKS relay. When lookup is non-nil and the address uses a
// hostname, resolves with lookup (Android DNS); otherwise uses net.Dialer (Go resolver when lookup is nil).
func dialConnectTarget(ctx context.Context, network, address string, lookup HostLookup) (net.Conn, error) {
	d := net.Dialer{Timeout: 30 * time.Second}
	netw := network
	if netw == "" {
		netw = "tcp"
	}
	if lookup == nil {
		return d.DialContext(ctx, netw, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return d.DialContext(ctx, netw, address)
	}
	if net.ParseIP(host) != nil {
		return d.DialContext(ctx, netw, address)
	}
	// Only TCP-style networks support hostname resolution via IP list; otherwise fall back.
	switch netw {
	case "tcp", "tcp4", "tcp6":
	default:
		return d.DialContext(ctx, netw, address)
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		switch netw {
		case "tcp4":
			if ip.To4() == nil {
				continue
			}
		case "tcp6":
			if ip.To4() != nil {
				continue
			}
		}
		addr := net.JoinHostPort(ip.String(), port)
		dialNet := "tcp"
		if netw == "tcp4" || netw == "tcp6" {
			dialNet = netw
		}
		c, err := d.DialContext(ctx, dialNet, addr)
		if err == nil {
			return c, nil
		}
		firstErr = err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("dial %s %q: no usable address from lookup", netw, address)
}
