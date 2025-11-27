package main

import (
	"github.com/DevN0mad/OpenProjectBot/internal/services"
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Список ids проектов 16 - android, 19 - СВОД, 5 - Сервер репей
	projectIDs := []string{"16", "19", "5"}

	// Список ids исполнителей
	assigneeIDs := []string{"20", "8", "5", "9", "12", "14", "27", "15", "29", "25", "11", "13", "10", "16", "17", "28"}

	opts := services.OpenProjectOpts{
		BaseURL:  "http://192.168.101.21",
		ApiToken: "8fffc4ea73a79304ca3ede354f9828b066a5dc46fa804c9912c0f4ba26575a70", // Павел (token)
		//ApiToken: "1efdfbb38ce78e6ed606aecc0fb899c1e0883e9fb3875ecbaf68745603c357f6", // Владислав (token)
		//ApiToken:    "fab71bfbcdaa046d7438c51e5d66e6dc3e173286f78165d7dd793041c05ea", // Кирилл (token)
		ProjectIDs:  projectIDs,
		AssigneeIDs: assigneeIDs,
		SaveDir:     "/home/gvladislav/Work/OpenProjectBot", // здесь нужен путь для сохранения файла
	}

	// Используем Basic Auth с apikey и API токеном
	opService := services.Init(opts, logger)

	logger.Info("Testing Basic Auth with API token")

	// Генерируем Excel отчет
	err := opService.GenerateExcelReport()
	if err != nil {
		logger.Error("Failed to generate report", "error", err)
		return
	}

	logger.Info("✅ Excel report successfully created", "file", "text_report.xlsx")
}
