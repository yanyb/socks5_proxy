package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"net"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"xsocks5/server/config"
	"xsocks5/server/hub"
	"xsocks5/server/logger"

	"github.com/sirupsen/logrus"
	"github.com/things-go/go-socks5"
)

func main() {
	cfgPath := flag.String("config", "configs/server.yaml", "server-only config: .yaml or .json")
	flag.Parse()

	srvCfg, err := config.LoadServer(*cfgPath)
	if err != nil {
		logrus.WithError(err).Fatal("load config")
	}

	deviceLog, closeDeviceLog, err := logger.Build(srvCfg.LogLevel, srvCfg.LogFormat, srvCfg.DeviceLogFile, "device")
	if err != nil {
		logrus.WithError(err).Fatal("init device logger")
	}
	defer closeDeviceLog()

	socksLog, closeSocksLog, err := logger.Build(srvCfg.LogLevel, srvCfg.LogFormat, srvCfg.SocksLogFile, "socks")
	if err != nil {
		logrus.WithError(err).Fatal("init socks logger")
	}
	defer closeSocksLog()

	bootLog := deviceLog.WithField("component", "boot")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := hub.NewRegistry()

	cert, err := tls.LoadX509KeyPair(srvCfg.TLSCertFile, srvCfg.TLSKeyFile)
	if err != nil {
		bootLog.WithError(err).Fatal("tls: load cert")
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	devLn, err := tls.Listen("tcp", srvCfg.DeviceListen, tlsConf)
	if err != nil {
		bootLog.WithError(err).Fatal("device listen")
	}

	socksLn, err := net.Listen("tcp", srvCfg.SocksListen)
	if err != nil {
		_ = devLn.Close()
		bootLog.WithError(err).Fatal("socks5 listen")
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		acceptLog := deviceLog.WithField("component", "device_listener")
		for {
			c, err := devLn.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					acceptLog.Debug("listener closed")
					return
				}
				acceptLog.WithError(err).Warn("accept")
				return
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				hub.ServeDevice(conn, reg, srvCfg.SessionHeartbeatTimeout, deviceLog)
			}(c)
		}
	}()

	opts := []socks5.Option{
		socks5.WithLogger(socksLog),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, req *socks5.Request) (net.Conn, error) {
			user := socksUsername(req)
			targetID, err := reg.ResolveDeviceForDial(user)
			if err != nil {
				return nil, err
			}
			return hub.DialThroughDevice(
				ctx,
				reg,
				socksLog,
				targetID,
				srvCfg.DeviceWaitTimeout,
				srvCfg.ConnectResultTimeout,
				network,
				addr,
			)
		}),
	}
	if srvCfg.SocksAuthPassword != "" {
		opts = append(opts, socks5.WithCredential(&hub.SOCKSPlainAuth{Password: srvCfg.SocksAuthPassword}))
	}
	s5 := socks5.NewServer(opts...)

	socksDone := make(chan struct{})
	go func() {
		defer close(socksDone)
		if err := s5.Serve(socksLn); err != nil && !errors.Is(err, net.ErrClosed) {
			bootLog.WithError(err).Error("socks5 serve")
		}
	}()

	authHint := "noauth (only valid when exactly one device is online)"
	if srvCfg.SocksAuthPassword != "" {
		authHint = "user/pass (username = device_id)"
	}
	bootLog.WithFields(logrus.Fields{
		"socks5":           srvCfg.SocksListen,
		"device_tls":       srvCfg.DeviceListen,
		"socks_auth":       authHint,
		"online_devices":   reg.ListOnline(),
		"device_log_file":  orStdout(srvCfg.DeviceLogFile),
		"socks_log_file":   orStdout(srvCfg.SocksLogFile),
		"shutdown_timeout": srvCfg.ShutdownTimeout.String(),
	}).Info("server started")

	<-ctx.Done()
	bootLog.Info("shutdown requested")
	stop() // restore default handler so a second Ctrl-C terminates immediately

	// 1) Stop accepting new work.
	if err := devLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		bootLog.WithError(err).Warn("close device listener")
	}
	if err := socksLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		bootLog.WithError(err).Warn("close socks listener")
	}

	// 2) Tear down active device sessions; in-flight SOCKS streams unblock once
	//    their underlying yamux session goes away.
	reg.CloseAll()

	// 3) Wait for goroutines (device accept loop + per-device + socks Serve).
	doneAll := make(chan struct{})
	go func() {
		wg.Wait()
		<-socksDone
		close(doneAll)
	}()

	timeout := srvCfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-doneAll:
		bootLog.Info("clean shutdown")
	case <-time.After(timeout):
		bootLog.WithField("timeout", timeout.String()).Warn("shutdown timed out, exiting")
	}
}

func socksUsername(req *socks5.Request) string {
	if req == nil || req.AuthContext == nil || req.AuthContext.Payload == nil {
		return ""
	}
	return strings.TrimSpace(req.AuthContext.Payload["username"])
}

func orStdout(p string) string {
	if strings.TrimSpace(p) == "" {
		return "stdout"
	}
	return p
}
