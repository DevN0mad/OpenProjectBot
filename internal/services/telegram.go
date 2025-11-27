package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/storage"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramOpts — параметры инициализации TelegramBotService.
type TelegramOpts struct {
	Token   string `mapstructure:"token"   validate:"required"`
	Message string `mapstructure:"message" validate:"required"`
	DbPath  string `mapstructure:"db_path" validate:"required"`
}

// TelegramBotService — сервис взаимодействия с Telegram.
type TelegramBotService struct {
	opts   TelegramOpts
	logger *slog.Logger
	bot    *tgbotapi.BotAPI
	store  *storage.BotStorage
	offset int
}

// NewTelegramBot создаёт экземпляр TelegramBotService.
func NewTelegramBot(opts TelegramOpts, logger *slog.Logger) (*TelegramBotService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if opts.Token == "" {
		return nil, fmt.Errorf("telegram bot token is required")
	}
	if opts.DbPath == "" {
		return nil, fmt.Errorf("telegram bot db path is required")
	}

	bot, err := tgbotapi.NewBotAPI(opts.Token)
	if err != nil {
		logger.Error("failed to create Telegram bot", "error", err)
		return nil, fmt.Errorf("create Telegram bot: %w", err)
	}

	store, err := storage.NewBotStorage(opts.DbPath, logger)
	if err != nil {
		logger.Error("failed to init bot storage", "error", err)
		return nil, fmt.Errorf("init bot storage: %w", err)
	}

	logger.Info("Telegram bot created successfully", "bot_user", bot.Self.UserName)

	return &TelegramBotService{
		opts:   opts,
		logger: logger,
		bot:    bot,
		store:  store,
		offset: 0,
	}, nil
}

// SendFile — отправляет файл всем сохранённым чатам.
func (s *TelegramBotService) SendFile(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if _, err := os.Stat(path); err != nil {
		s.logger.Error("file not found or inaccessible", "path", path, "error", err)
		return fmt.Errorf("file not found or inaccessible %q: %w", path, err)
	}

	chats, err := s.store.ListChats()
	if err != nil {
		s.logger.Error("failed to get chat list", "error", err)
		return fmt.Errorf("list chats: %w", err)
	}

	if len(chats) == 0 {
		s.logger.Warn("no chats found to send file to")
		return nil
	}

	for _, chatID := range chats {
		select {
		case <-ctx.Done():
			s.logger.Info("send interrupted by context cancel")
			return ctx.Err()
		default:
		}

		msg := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
		msg.Caption = s.opts.Message

		if _, err := s.bot.Send(msg); err != nil {
			s.logger.Error("failed to send file", "chat_id", chatID, "path", path, "error", err)
			continue
		}
		s.logger.Info("file sent successfully", "chat_id", chatID, "path", path)
	}

	return nil
}

// Start — запускает long polling цикл получения новых данных.
func (s *TelegramBotService) Start(ctx context.Context) {
	s.logger.Info("starting Telegram long polling loop")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("long polling stopped", "reason", ctx.Err())
			return
		default:
		}

		updates, err := s.bot.GetUpdates(tgbotapi.UpdateConfig{
			Offset:         s.offset + 1,
			Timeout:        60,
			AllowedUpdates: []string{"my_chat_member"},
		})
		if err != nil {
			if ctx.Err() != nil {
				s.logger.Info("shutdown requested, stop polling", "error", ctx.Err())
				return
			}
			s.logger.Error("long polling error", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(updates) == 0 {
			continue
		}

		s.logger.Debug("получены обновления", "count", len(updates))

		for _, u := range updates {
			if u.MyChatMember != nil {
				s.handleMyChatMember(ctx, u.MyChatMember)
			}
			if u.UpdateID > s.offset {
				s.offset = u.UpdateID
			}
		}
	}
}

// handleMyChatMember — обрабатывает добавление/удаление бота из чата.
func (s *TelegramBotService) handleMyChatMember(ctx context.Context, m *tgbotapi.ChatMemberUpdated) {
	chat := m.Chat
	if chat.Type == "supergroup" && chat.Title == "" {
		s.logger.Debug("ignore forum topic in supergroup", "chat_id", chat.ID)
		return
	}

	status := m.NewChatMember.Status
	switch status {
	case "member", "administrator":
		if err := s.store.SaveChat(ctx, chat.ID, chat.Title); err != nil {
			s.logger.Error("failed to save chat", "chat_id", chat.ID, "error", err)
		} else {
			s.logger.Info("chat saved", "chat_id", chat.ID, "title", chat.Title)
		}
	case "left", "kicked":
		if err := s.store.RemoveChat(ctx, chat.ID); err != nil {
			s.logger.Error("failed to remove chat", "chat_id", chat.ID, "error", err)
		} else {
			s.logger.Info("chat removed", "chat_id", chat.ID)
		}
	}
}
