package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"xsocks5/client/config"
	"xsocks5/client/core"
)

func main() {
	cfgPath := flag.String("config", "configs/client.yaml", "device config file: .yaml or .json (JSON matches API / ParseClientJSON)")
	flag.Parse()

	cfg, err := config.LoadClient(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stdout, "client: ", log.LstdFlags)
	if err := core.Run(ctx, cfg, logger); err != nil && err != context.Canceled {
		log.Fatalf("client: %v", err)
	}
}
