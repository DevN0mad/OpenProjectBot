package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/DevN0mad/OpenProjectBot/internal/config"
	"github.com/DevN0mad/OpenProjectBot/internal/services"
)

// App представляет основное приложение, управляющее сервисами.
type App struct {
	logger *slog.Logger

	mu          sync.Mutex
	tg          *services.TelegramBotService
	dailyJob    *services.DailyJobService
	dailyCancel context.CancelFunc
}

// NewApp создает новый экземпляр приложения с заданным логгером.
func NewApp(logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	return &App{logger: logger}
}

// ApplyConfig применяет переданную конфигурацию к приложению, инициализируя или переинициализируя сервисы.
func (a *App) ApplyConfig(cfg config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.dailyCancel != nil {
		a.logger.Info("Stopping previous DailyJob")
		a.dailyCancel()
		a.dailyCancel = nil
	}

	tg, err := services.NewTelegramBot(cfg.TelegramBot, a.logger)
	if err != nil {
		return fmt.Errorf("init telegram bot: %w", err)
	}

	dailyJob, err := services.NewDailyJobService(tg, cfg.DailyJob, a.logger)
	if err != nil {
		return fmt.Errorf("init daily job: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go dailyJob.Start(ctx)

	a.tg = tg
	a.dailyJob = dailyJob
	a.dailyCancel = cancel

	a.logger.Info("Services reinitialized from config")
	return nil
}

// Shutdown корректно останавливает все запущенные сервисы приложения.
func (a *App) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.dailyCancel != nil {
		a.logger.Info("Stopping DailyJob on shutdown")
		a.dailyCancel()
		a.dailyCancel = nil
	}
}
