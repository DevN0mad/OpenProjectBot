package config

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"

	"github.com/DevN0mad/OpenProjectBot/internal/server"
	"github.com/DevN0mad/OpenProjectBot/internal/services"
)

// Config представляет конфигурацию приложения.
type Config struct {
	TelegramBot services.TelegramOpts    `mapstructure:"telegram_bot"`
	DailyJob    services.DailyJobOpts    `mapstructure:"daily_job"`
	OpenProject services.OpenProjectOpts `mapstructure:"open_project"`
	HttpServer  server.AdminServerOpts   `mapstructure:"http_server"`
}

// Manager управляет конфигурацией приложения, обеспечивая загрузку,
type Manager struct {
	mu          sync.RWMutex
	cfg         *Config
	logger      *slog.Logger
	v           *viper.Viper
	subscribers []func(Config)
	validate    *validator.Validate
}

// NewManager создает новый менеджер конфигурации, загружая конфигурацию из указанного пути.
func NewManager(path string, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	m := &Manager{
		cfg:      &cfg,
		logger:   logger,
		v:        v,
		validate: validator.New(),
	}
	err := m.validate.Struct(&cfg)
	if err != nil {
		logger.Error("Validate config", "error", err)
		return nil, fmt.Errorf("validate config: %w", err)
	}

	logger.Info("Config loaded", "path", path)

	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		logger.Info("Config file changed", "name", e.Name, "op", e.Op.String())

		var newCfg Config
		if err := v.Unmarshal(&newCfg); err != nil {
			logger.Error("Failed to reload config", "error", err)
			return
		}

		if err := m.validate.Struct(&newCfg); err != nil {
			logger.Error("Validate reloaded config", "error", err)
			return
		}

		m.mu.Lock()
		m.cfg = &newCfg
		subs := append([]func(Config){}, m.subscribers...)
		m.mu.Unlock()

		logger.Info("Config reloaded successfully")

		for _, fn := range subs {
			fn(newCfg)
		}
	})

	return m, nil
}

// Current возвращает текущую конфигурацию.
func (m *Manager) Current() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.cfg
}

// OnChange регистрирует функцию обратного вызова, которая будет вызвана при изменении конфигурации.
func (m *Manager) OnChange(fn func(Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers = append(m.subscribers, fn)
}
