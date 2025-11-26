package logger

import (
	"log/slog"
	"os"
)

func InitLogger() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
}
