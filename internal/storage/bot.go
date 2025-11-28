package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type BotStorage struct {
	db     *gorm.DB
	logger *slog.Logger
}

func NewBotStorage(dbPath string, logger *slog.Logger) (*BotStorage, error) {
	if logger == nil {
		logger = slog.Default()
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("failed to create db dir", "dir", dir, "error", err)
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		logger.Error("failed to open sqlite db", "path", dbPath, "error", err)
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.AutoMigrate(&models.Chat{}); err != nil {
		logger.Error("failed to auto-migrate chat model", "error", err)
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	logger.Info("sqlite bot storage initialized", "path", dbPath)

	return &BotStorage{db: db, logger: logger}, nil
}

func (s *BotStorage) SaveChat(ctx context.Context, chatID int64, title string) error {
	db := s.db.WithContext(ctx)

	var chat models.Chat
	if err := db.Where("chat_id = ?", chatID).First(&chat).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			chat = models.Chat{
				ChatID:  chatID,
				Title:   title,
				AddedAt: time.Now(),
			}
			if err := db.Create(&chat).Error; err != nil {
				s.logger.Error("failed to create chat", "chat_id", chatID, "title", title, "error", err)
				return fmt.Errorf("create chat: %w", err)
			}
			s.logger.Info("chat created", "chat_id", chatID, "title", title)
			return nil
		}

		s.logger.Error("failed to load chat", "chat_id", chatID, "error", err)
		return err
	}

	chat.Title = title
	chat.AddedAt = time.Now()
	if err := db.Save(&chat).Error; err != nil {
		s.logger.Error("failed to update chat", "chat_id", chatID, "title", title, "error", err)
		return err
	}

	s.logger.Info("chat updated", "chat_id", chatID, "title", title)
	return nil
}

func (s *BotStorage) RemoveChat(ctx context.Context, chatID int64) error {
	db := s.db.WithContext(ctx)

	if err := db.Where("chat_id = ?", chatID).Delete(&models.Chat{}).Error; err != nil {
		s.logger.Error("failed to remove chat", "chat_id", chatID, "error", err)
		return err
	}

	s.logger.Info("chat removed", "chat_id", chatID)
	return nil
}

// если хочешь тоже через контекст — добавь ctx параметром
func (s *BotStorage) ListChats() ([]int64, error) {
	var ids []int64
	if err := s.db.Model(&models.Chat{}).Pluck("chat_id", &ids).Error; err != nil {
		s.logger.Error("failed to list chats", "error", err)
		return nil, err
	}
	return ids, nil
}

func (s *BotStorage) GetBotChats(ctx context.Context) ([]models.Chat, error) {
	db := s.db.WithContext(ctx)

	var chats []models.Chat
	if err := db.Find(&chats).Error; err != nil {
		s.logger.Error("failed to select chats", "error", err)
		return []models.Chat{}, err
	}

	if len(chats) == 0 {
		s.logger.Info("no chats found")
	}

	return chats, nil
}

func (s *BotStorage) UpdateChatID(ctx context.Context, oldChatID, newChatID int64) error {
	db := s.db.WithContext(ctx)

	res := db.Model(&models.Chat{}).
		Where("chat_id = ?", oldChatID).
		Updates(map[string]any{
			"chat_id":  newChatID,
			"added_at": time.Now(),
		})

	if res.Error != nil {
		s.logger.Error("failed to update chat_id",
			"old_chat_id", oldChatID,
			"new_chat_id", newChatID,
			"error", res.Error)
		return res.Error
	}

	s.logger.Debug("chat_id updated",
		"old_chat_id", oldChatID,
		"new_chat_id", newChatID,
		"rows_affected", res.RowsAffected)

	return nil
}
