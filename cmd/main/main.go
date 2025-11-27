package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/DevN0mad/OpenProjectBot/internal/config"
	"github.com/DevN0mad/OpenProjectBot/internal/core"
)

var (
	configPath = flag.String("config", "/etc/open_project_bot/config.yaml", "Путь к файлу с конфигурацией")
)

func main() {
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfgMgr, err := config.NewManager(*configPath, logger)
	if err != nil {
		logger.Error("Failed to init config manager", "error", err)
		os.Exit(1)
	}

	app := core.NewApp(ctx, logger)

	if err := app.ApplyConfig(cfgMgr.Current()); err != nil {
		logger.Error("Failed to apply initial config", "error", err)
		os.Exit(1)
	}

	cfgMgr.OnChange(func(newCfg config.Config) {
		if err := app.ApplyConfig(newCfg); err != nil {
			logger.Error("Failed to apply new config", "error", err)
		}
	})

	<-ctx.Done()
	logger.Info("Shutdown requested", "reason", ctx.Err())

	app.Shutdown()
	logger.Info("Shutdown complete")
}
