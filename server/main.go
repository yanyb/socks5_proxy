package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"xsocks5/server/config"
	"xsocks5/server/hub"

	"github.com/things-go/go-socks5"
)

func main() {
	cfgPath := flag.String("config", "configs/server.yaml", "server-only config: .yaml or .json")
	flag.Parse()

	srvCfg, err := config.LoadServer(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	reg := hub.NewRegistry()

	cert, err := tls.LoadX509KeyPair(srvCfg.TLSCertFile, srvCfg.TLSKeyFile)
	if err != nil {
		log.Fatalf("tls: load cert: %v", err)
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	devLn, err := tls.Listen("tcp", srvCfg.DeviceListen, tlsConf)
	if err != nil {
		log.Fatalf("device listen: %v", err)
	}
	defer devLn.Close()

	go func() {
		logger := log.New(os.Stdout, "device: ", log.LstdFlags|log.Lmsgprefix)
		for {
			c, err := devLn.Accept()
			if err != nil {
				log.Printf("device accept: %v", err)
				return
			}
			go hub.ServeDevice(c, reg, srvCfg.SessionHeartbeatTimeout, logger)
		}
	}()

	opts := []socks5.Option{
		socks5.WithLogger(socks5.NewLogger(log.New(os.Stdout, "socks5: ", log.LstdFlags))),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, req *socks5.Request) (net.Conn, error) {
			user := socksUsername(req)
			targetID, err := reg.ResolveDeviceForDial(user)
			if err != nil {
				return nil, err
			}
			return hub.DialThroughDevice(
				ctx,
				reg,
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

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		_ = devLn.Close()
		os.Exit(0)
	}()

	authHint := "noauth (only valid when exactly one device is online)"
	if srvCfg.SocksAuthPassword != "" {
		authHint = "user/pass (username = device_id)"
	}
	log.Printf("server: socks5=%s device_tls=%s socks_auth=%s online_devices=%v",
		srvCfg.SocksListen, srvCfg.DeviceListen, authHint, reg.ListOnline())

	if err := s5.ListenAndServe("tcp", srvCfg.SocksListen); err != nil {
		log.Fatalf("socks5: %v", err)
	}
}

func socksUsername(req *socks5.Request) string {
	if req == nil || req.AuthContext == nil || req.AuthContext.Payload == nil {
		return ""
	}
	return strings.TrimSpace(req.AuthContext.Payload["username"])
}
