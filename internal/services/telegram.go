package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramOpts параметры необходимые для инициализации сервиса TelegramBotService.
type TelegramOpts struct {
	Token   string `yaml:"token" validate:"required"`
	ChatID  int64  `yaml:"chat_id" validate:"required"`
	Message string `yaml:"message" validate:"required"`
}

// TelegramBotService сервис предназначенный для взаимодействия с telegram.
type TelegramBotService struct {
	opts   TelegramOpts
	logger *slog.Logger
	bot    *tgbotapi.BotAPI
}

// NewTelegramBot создает экземпляр сервиса для работы с telegram ботом.
func NewTelegramBot(opts TelegramOpts, logger *slog.Logger) (*TelegramBotService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if opts.Token == "" {
		return nil, fmt.Errorf("telegram bot token is required")
	}

	if opts.ChatID == 0 {
		return nil, fmt.Errorf("telegram chat id is required")
	}

	bot, err := tgbotapi.NewBotAPI(opts.Token)
	if err != nil {
		logger.Error("Failed to create Telegram bot", "error", err)
		return nil, fmt.Errorf("create Telegram bot: %w", err)
	}

	logger.Info("Telegram bot created successfully",
		"bot_user", bot.Self.UserName,
		"chat_id", opts.ChatID,
	)
	return &TelegramBotService{
		opts:   opts,
		logger: logger,
		bot:    bot,
	}, nil
}

// SendFile отправляет файл по переданному пути в telegram чат.
func (s *TelegramBotService) SendFile(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			s.logger.Error("File not found", "path", path, "error", err)
			return fmt.Errorf("file not found at %q: %w", path, err)
		}
		s.logger.Error("Failed to access file", "path", path, "error", err)
		return fmt.Errorf("access file at %q: %w", path, err)
	}

	msg := tgbotapi.NewDocument(s.opts.ChatID, tgbotapi.FilePath(path))
	msg.Caption = s.opts.Message

	if _, err := s.bot.Send(msg); err != nil {
		s.logger.Error("Failed to send file",
			"path", path,
			"chat_id", s.opts.ChatID,
			"error", err)
		return fmt.Errorf("send file: %w", err)
	}

	s.logger.Info("File sent successfully",
		"path", path,
		"chat_id", s.opts.ChatID)
	return nil
}
