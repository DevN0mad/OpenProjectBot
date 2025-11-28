package models

import "time"

type Chat struct {
	ID      uint      `gorm:"column:id;primaryKey" db:"id"`
	ChatID  int64     `gorm:"column:chat_id;uniqueIndex;not null" db:"chat_id"`
	Title   string    `gorm:"column:title;not null" db:"title"`
	AddedAt time.Time `gorm:"column:added_at;autoCreateTime" db:"added_at"`
}

func (Chat) TableName() string {
	return "chats"
}
