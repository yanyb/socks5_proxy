package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"my_socks5_proxy/internal/client"
	"my_socks5_proxy/internal/config"
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
	if err := client.Run(ctx, cfg, logger); err != nil && err != context.Canceled {
		log.Fatalf("client: %v", err)
	}
}
