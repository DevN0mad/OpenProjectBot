package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/config"
	"github.com/DevN0mad/OpenProjectBot/internal/core"
)

var (
	configPath = flag.String("config", "/etc/open_project_bot/config.yaml", "Путь к файлу с конфигурацией")
)

func main() {
	flag.Parse()

	opts := &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				t := a.Value.Time()
				a.Value = slog.StringValue(t.Local().Format(time.TimeOnly))

			case slog.SourceKey:
				if src, ok := a.Value.Any().(*slog.Source); ok {
					a.Value = slog.StringValue(
						fmt.Sprintf("%s:%d", path.Base(src.File), src.Line),
					)
				}
			}
			return a
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))

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
