package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/DevN0mad/OpenProjectBot/internal/config"
	"github.com/DevN0mad/OpenProjectBot/internal/server"
	"github.com/DevN0mad/OpenProjectBot/internal/services"
)

// App представляет основное приложение, управляющее сервисами.
type App struct {
	logger  *slog.Logger
	rootCtx context.Context

	mu             sync.Mutex
	tg             *services.TelegramBotService
	opSrv          *services.OpenProjectService
	dailyJob       *services.DailyJobService
	adminSrv       *server.AdminServer
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

	opSrv := services.Init(cfg.OpenProject, a.logger)
	if opSrv == nil {
		cancel()
		a.logger.Error("init open project service", "error", "open project service is nil")
		return fmt.Errorf("init open project service: %s", "open project service is nil")
	}

	dailyJob, err := services.NewDailyJobService(tg, opSrv, cfg.DailyJob, a.logger)
	if err != nil {
		cancel()
		return fmt.Errorf("init daily job: %w", err)
	}

	adminSrv := server.NewAdminHandler(a.logger, opSrv, tg, &cfg.HttpServer)

	go tg.Start(ctx)
	go dailyJob.Start(ctx)
	go func() {
		if err := adminSrv.Start(ctx); err != nil {
			a.logger.Error("Admin server exited with error", "error", err)
		}
	}()

	a.tg = tg
	a.dailyJob = dailyJob
	a.opSrv = opSrv
	a.adminSrv = adminSrv
	a.servicesCancel = cancel

	a.logger.Info("Services reinitialized successfully with configuration")
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
