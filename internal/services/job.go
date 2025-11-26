package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DailyJobOpts параметры необходимые для работы сервиса.
type DailyJobOpts struct {
	FilePath string `yaml:"file_path" validate:"required"`
	Hour     int    `yaml:"hour" validate:"required,min=0,max=23"`
	Minute   int    `yaml:"minute" validate:"required,min=0,max=59"`
}

// DailyJobService отправляет файл каждый день в заданное время.
type DailyJobService struct {
	botServ  *TelegramBotService
	filePath string
	hour     int
	minute   int
	timezone *time.Location
	logger   *slog.Logger
	// TODO добавить сервис по созданию отчета
}

// NewDailyJobService создаёт сервис для ежедневной отправки файлов.
func NewDailyJobService(
	botServ *TelegramBotService,
	// TODO добавить сервис по созданию отчета
	opts DailyJobOpts,
	logger *slog.Logger,
) (*DailyJobService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if botServ == nil {
		return nil, fmt.Errorf("bot service is required")
	}

	if opts.FilePath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	logger.Info("Daily job configured",
		"hour", opts.Hour,
		"minute", opts.Minute,
		"timezone", time.Local.String(),
		"file", opts.FilePath)

	return &DailyJobService{
		botServ:  botServ,
		filePath: opts.FilePath,
		hour:     opts.Hour,
		minute:   opts.Minute,
		timezone: time.Local,
		logger:   logger,
	}, nil
}

// Start запускает цикл отправки.
func (d *DailyJobService) Start(ctx context.Context) {
	nextRun := d.nextRunTime()
	timer := time.NewTimer(time.Until(nextRun))
	d.logger.Info("Next run scheduled", "at", nextRun.Format(time.RFC3339))

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Shutdown requested")
			timer.Stop()
			return
		case <-timer.C:
			// TODO вызов сервиса по созданию отчета и получение пути к файлу
			if err := d.botServ.SendFile(ctx, d.filePath); err != nil {
				d.logger.Error("Daily report sending failed", "error", err)
			} else {
				d.logger.Info("Daily report sent successfully")
			}

			nextRun = d.nextRunTime()
			timer.Reset(time.Until(nextRun))
			d.logger.Info("Next run scheduled", "at", nextRun.Format(time.RFC3339))
		}
	}
}

// nextRunTime вычисляет ближайшее время
func (d *DailyJobService) nextRunTime() time.Time {
	now := time.Now().In(d.timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), d.hour, d.minute, 0, 0, d.timezone)

	if now.After(today) {
		return today.Add(24 * time.Hour)
	}
	return today
}
