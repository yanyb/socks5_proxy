// admin: HTTP service that registers devices and persists them to MongoDB.
//
//	Endpoints:
//	  POST /register   -> see admin/handler.RegisterHandler
//	  GET  /healthz    -> "ok"
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"xsocks5/admin/config"
	"xsocks5/admin/handler"
	"xsocks5/admin/nsqcons"
	"xsocks5/admin/store"
	"xsocks5/server/logger"

	"github.com/sirupsen/logrus"
)

func main() {
	cfgPath := flag.String("config", "configs/admin.yaml", "admin config: .yaml or .json")
	flag.Parse()

	cfg, err := config.LoadAdmin(*cfgPath)
	if err != nil {
		logrus.WithError(err).Fatal("admin: load config")
	}

	log, closeLog, err := logger.Build(cfg.LogLevel, cfg.LogFormat, cfg.LogFile, "admin")
	if err != nil {
		logrus.WithError(err).Fatal("admin: init logger")
	}
	defer closeLog()
	bootLog := log.WithField("component", "boot")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mstore, err := store.NewMongoDeviceStore(ctx, cfg.MongoURI, cfg.MongoDB, cfg.DeviceCollection)
	if err != nil {
		bootLog.WithError(err).Fatal("admin: connect mongo")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mstore.Close(closeCtx)
	}()

	regHandler, err := handler.New(cfg, mstore, log)
	if err != nil {
		bootLog.WithError(err).Fatal("admin: init handler")
	}

	// Heartbeat NSQ consumer (optional; empty lookupd list disables it).
	var hbCons *nsqcons.Consumer
	if len(cfg.NSQLookupdHTTPAddrs) > 0 {
		c, err := nsqcons.New(nsqcons.Config{
			Topic:           cfg.HeartbeatTopic,
			Channel:         cfg.HeartbeatChannel,
			LookupdHTTPAddr: cfg.NSQLookupdHTTPAddrs,
			Concurrency:     cfg.HeartbeatConcurrency,
		}, mstore, log)
		if err != nil {
			bootLog.WithError(err).Fatal("admin: init nsq consumer")
		}
		if err := c.Start(); err != nil {
			bootLog.WithError(err).Fatal("admin: start nsq consumer")
		}
		hbCons = c
		defer c.Stop()
	}

	mux := http.NewServeMux()
	mux.Handle("/register", regHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.HTTPListen,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	bootLog.WithFields(logrus.Fields{
		"http_listen":       cfg.HTTPListen,
		"mongo_uri":         cfg.MongoURI,
		"mongo_db":          cfg.MongoDB,
		"device_collection": cfg.DeviceCollection,
		"server_addr_out":   cfg.ServerAddr,
		"heartbeat_sec":     cfg.HeartbeatIntervalSec,
		"max_clock_skew_s":  cfg.MaxClockSkewSec,
		"nsq_lookupd":       cfg.NSQLookupdHTTPAddrs,
		"nsq_consumer":      hbCons != nil,
	}).Info("admin started")

	serveErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		bootLog.Info("shutdown requested")
	case err := <-serveErr:
		if err != nil {
			bootLog.WithError(err).Error("http serve")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		bootLog.WithError(err).Warn("http shutdown")
	}
	bootLog.Info("clean shutdown")
}
