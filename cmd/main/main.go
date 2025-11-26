package main

import (
	"context"
	"flag"
	"github.com/DevN0mad/OpenProjectBot/internal/core"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/DevN0mad/OpenProjectBot/internal/config"
)

var (
	configPath = flag.String("config", "/etc/open_project_bot/config.yaml", "Путь к файлу с конфигурацией")
)

func main() {
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfgMgr, err := config.NewManager(*configPath, logger)
	if err != nil {
		logger.Error("Failed to init config manager", "error", err)
		os.Exit(1)
	}

	a := core.NewApp(logger)

	if err := a.ApplyConfig(cfgMgr.Current()); err != nil {
		logger.Error("Failed to apply initial config", "error", err)
		os.Exit(1)
	}

	cfgMgr.OnChange(func(newCfg config.Config) {
		if err := a.ApplyConfig(newCfg); err != nil {
			logger.Error("Failed to apply new config", "error", err)
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("Shutdown requested")
	a.Shutdown()
	logger.Info("Shutdown complete")
}
