package models

import "time"

type Chat struct {
	ID      uint      `gorm:"primaryKey"`
	ChatID  int64     `gorm:"uniqueIndex;not null"`
	Title   string    `gorm:"not null"`
	AddedAt time.Time `gorm:"autoCreateTime"`
}
