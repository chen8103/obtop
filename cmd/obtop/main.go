package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/chen8103/obtop/internal/config"
	"github.com/chen8103/obtop/internal/logx"
	"github.com/chen8103/obtop/internal/obclient"
	"github.com/chen8103/obtop/internal/service"
	"github.com/chen8103/obtop/internal/ui"
)

func main() {
	if handled, code := runVersion(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}

	logger := logx.New()

	if len(os.Args) > 1 && isMachineCommand(os.Args[1]) {
		os.Exit(runMachineCommand(os.Args[1:]))
	}

	cfg, err := config.Parse(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Println(err)
		config.WriteUsage(os.Stderr)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := obclient.New(ctx, cfg)
	if err != nil {
		logger.Printf("connect to OceanBase: %v", err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Printf("close database: %v", err)
		}
	}()

	monitor := service.NewMonitor(client, cfg.Interval)
	app := ui.New(cfg, monitor, client, stop)

	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Printf("run ui: %v", err)
		os.Exit(1)
	}
}
