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
	logger  *slog.Logger
	rootCtx context.Context

	mu             sync.Mutex
	tg             *services.TelegramBotService
	dailyJob       *services.DailyJobService
	servicesCancel context.CancelFunc
}

// NewApp создает новый экземпляр приложения с заданным логгером и корневым контекстом.
func NewApp(ctx context.Context, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &App{
		logger:  logger,
		rootCtx: ctx,
	}
}

// ApplyConfig применяет конфигурацию к приложению, инициализируя/переинициализируя сервисы.
func (a *App) ApplyConfig(cfg config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.servicesCancel != nil {
		a.logger.Info("Stopping previous services")
		a.servicesCancel()
		a.servicesCancel = nil
	}

	ctx, cancel := context.WithCancel(a.rootCtx)

	tg, err := services.NewTelegramBot(cfg.TelegramBot, a.logger)
	if err != nil {
		cancel()
		return fmt.Errorf("init telegram bot: %w", err)
	}

	dailyJob, err := services.NewDailyJobService(tg, cfg.DailyJob, a.logger)
	if err != nil {
		cancel()
		return fmt.Errorf("init daily job: %w", err)
	}

	go tg.Start(ctx)
	go dailyJob.Start(ctx)

	a.tg = tg
	a.dailyJob = dailyJob
	a.servicesCancel = cancel

	a.logger.Info("Services reinitialized from config")
	return nil
}

// Shutdown останавливает все запущенные сервисы приложения.
func (a *App) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.servicesCancel != nil {
		a.logger.Info("Stopping services on shutdown")
		a.servicesCancel()
		a.servicesCancel = nil
	}
}
