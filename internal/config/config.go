package config

import "github.com/DevN0mad/OpenProjectBot/internal/services"

// Config структура конфигурации приложения.
type Config struct {
	TelegramBot services.TelegramOpts `yaml:"telegram_bot"`
	DailyJob    services.DailyJobOpts `yaml:"daily_job"`
}

func NewConfig(path string) *Config {
	// TODO загрузка конфигурации из файла или переменных окружения
	return &Config{}
}
