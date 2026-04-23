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

// HeartbeatSink receives a per-heartbeat snapshot from ServeDevice. The
// concrete implementation in server/main.go enriches with geo info and
// publishes to NSQ; tests pass nil or a recorder.
//
// Errors are logged by the caller; they do NOT prevent the ack being sent
// back to the device. We never want a downstream pipeline blip to look like
// a network failure to phones in the field.
type HeartbeatSink interface {
	OnHeartbeat(ctx context.Context, hb HeartbeatRecord) error
}

// HeartbeatRecord is what ServeDevice forwards to the sink. RemoteIP is the
// device's TCP source address (IPv4 or IPv6) as observed by this server.
type HeartbeatRecord struct {
	DeviceID     string
	RemoteIP     string
	NetType      string
	CurTsMs      int64
	ServerRecvMs int64
	AvgRTTms     *int64
	LossRate     *float64
}

// ServeDevice accepts one TLS+TCP connection from a device, runs yamux server,
// handles register + control stream. sink is optional (nil to disable downstream
// fan-out).
func ServeDevice(
	raw net.Conn,
	reg *Registry,
	heartbeatTimeout time.Duration,
	logger *logrus.Logger,
	sink HeartbeatSink,
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

	// Cache the parsed remote IP once per session; the host part of TCP
	// RemoteAddr doesn't change within a connection.
	remoteIP := parseRemoteHost(remote)

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
			recvMs := time.Now().UnixMilli()
			mu.Lock()
			lastBeat = time.Now()
			mu.Unlock()
			fields := logrus.Fields{"cur_ts": env.CurTs, "remote_ip": remoteIP}
			if env.NetType != "" {
				fields["net_type"] = env.NetType
			}
			if env.AvgRTT != nil {
				fields["avg_rtt_ms"] = *env.AvgRTT
			}
			if env.LossRate != nil {
				fields["loss_rate"] = *env.LossRate
			}
			devLog.WithFields(fields).Debug("recv heartbeat")
			// Echo cur_ts back so the client can compute RTT.
			if err := protocol.WriteLine(stream, &protocol.Envelope{
				Type:  protocol.TypeHeartbeatAck,
				Ts:    env.Ts,
				CurTs: env.CurTs,
			}); err != nil {
				devLog.WithError(err).Error("write heartbeat_ack")
			}
			// Fan-out to downstream pipeline (geo + NSQ). Never block the
			// device loop on it -- if the sink is slow we'd back-pressure
			// the device read side.
			if sink != nil {
				rec := HeartbeatRecord{
					DeviceID:     first.DeviceID,
					RemoteIP:     remoteIP,
					NetType:      env.NetType,
					CurTsMs:      env.CurTs,
					ServerRecvMs: recvMs,
					AvgRTTms:     env.AvgRTT,
					LossRate:     env.LossRate,
				}
				go func() {
					if err := sink.OnHeartbeat(ctx, rec); err != nil {
						devLog.WithError(err).Warn("heartbeat sink")
					}
				}()
			}
		default:
			devLog.WithField("type", env.Type).Debug("ignore unknown control frame")
		}
	}
}

// parseRemoteHost strips the port from a "host:port" string, supporting both
// IPv4 ("1.2.3.4:55555") and IPv6 ("[::1]:55555"). Returns the input unchanged
// if it doesn't look like an addr (extreme edge case).
func parseRemoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
