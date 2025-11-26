package main

import (
	"github.com/DevN0mad/OpenProjectBot/internal/services"
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Используем Basic Auth с apikey и API токеном
	opService := services.Init(
		"http://192.168.101.21",
		"1efdfbb38ce78e6ed606aecc0fb899c1e0883e9fb3875ecbaf68745603c357f6", // API токен
		"16", // project ID
	)

	logger.Info("Тестируем Basic Auth с API токеном")

	// Генерируем Excel отчет
	err := opService.GenerateExcelReport("test_report.xlsx")
	if err != nil {
		logger.Error("Ошибка генерации отчета", "error", err)
		return
	}

	logger.Info("✅ Excel отчет успешно создан: test_report.xlsx")
}
