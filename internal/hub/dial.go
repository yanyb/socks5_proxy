package hub

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"my_socks5_proxy/internal/protocol"
)

// DialThroughDevice opens a new yamux stream to the chosen device, performs connect handshake, and returns the stream for relay (implements net.Conn).
// targetDeviceID must be non-empty (see Registry.ResolveDeviceForDial).
func DialThroughDevice(
	ctx context.Context,
	reg *Registry,
	targetDeviceID string,
	deviceWait time.Duration,
	resultWait time.Duration,
	network, addr string,
) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("hub: only tcp connect is supported (got network=%q)", network)
	}

	waitCtx := ctx
	if deviceWait > 0 {
		var cancelWait context.CancelFunc
		waitCtx, cancelWait = context.WithTimeout(ctx, deviceWait)
		defer cancelWait()
	}

	sess, _, err := reg.WaitSession(waitCtx, targetDeviceID)
	if err != nil {
		return nil, fmt.Errorf("hub: no device session: %w", err)
	}

	stream, err := sess.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("hub: open stream: %w", err)
	}

	id := randomID()
	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type:    protocol.TypeConnect,
		ID:      id,
		Network: network,
		Address: addr,
	}); err != nil {
		_ = stream.Close()
		return nil, err
	}

	br := bufio.NewReader(stream)

	readCtx := ctx
	if resultWait > 0 {
		var cancelResult context.CancelFunc
		readCtx, cancelResult = context.WithTimeout(ctx, resultWait)
		defer cancelResult()
	}

	type res struct {
		env *protocol.Envelope
		err error
	}
	ch := make(chan res, 1)
	go func() {
		env, err := protocol.ReadLine(br)
		ch <- res{env, err}
	}()

	select {
	case <-readCtx.Done():
		_ = stream.Close()
		return nil, readCtx.Err()
	case r := <-ch:
		if r.err != nil {
			_ = stream.Close()
			return nil, r.err
		}
		if r.env.Type != protocol.TypeConnectResult || r.env.ID != id {
			_ = stream.Close()
			return nil, fmt.Errorf("hub: unexpected connect_result")
		}
		if !r.env.OK {
			_ = stream.Close()
			msg := r.env.Message
			if msg == "" {
				msg = "connect failed"
			}
			return nil, fmt.Errorf("hub: device: %s", msg)
		}
		return stream, nil
	}
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
