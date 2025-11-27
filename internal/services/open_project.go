package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/models"

	"github.com/xuri/excelize/v2"
)

// OpenProjectService основной сервис для работы с OpenProject

type OpenProjectOpts struct {
	BaseURL     string   `mapstructure:"baseURL" validate:"required"`
	ApiToken    string   `mapstructure:"apiToken" validate:"required"`
	ProjectIDs  []string `mapstructure:"projectIDs" validate:"required"`
	AssigneeIDs []string `mapstructure:"assigneeIDs" validate:"required"`
	SaveDir     string   `mapstructure:"saveDir" validate:"required"`
}

type OpenProjectService struct {
	opts   OpenProjectOpts
	logger *slog.Logger
	client *http.Client
}

// Init инициализирует сервис с API токеном
func Init(opts OpenProjectOpts, logger *slog.Logger) *OpenProjectService {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("ckech init func")
	return &OpenProjectService{
		opts:   opts,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *OpenProjectService) GetWorkPackagesByUsers() ([]models.WorkPackage, error) {
	var allWorkPackages []models.WorkPackage
	var mu sync.Mutex

	s.logger.Info("Starting parallel tasks export with limit",
		"projects_count", len(s.opts.ProjectIDs),
		"users_count", len(s.opts.AssigneeIDs))

	// Ограничиваем количество одновременных запросов
	semaphore := make(chan struct{}, 10)
	var wg sync.WaitGroup

	s.logger.Info("🔍 Получение задач по пользователям\n")

	// Для каждого проекта и пользователя
	for _, projectID := range s.opts.ProjectIDs {
		s.logger.Info("--- Проект ---", "project_id", projectID)

		for _, assigneeID := range s.opts.AssigneeIDs {
			wg.Add(1)

			go func(pid, uid string) {
				defer wg.Done()

				// Захват слота
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				userTasks, err := s.getWorkPackagesForUser(pid, uid)
				if err != nil {
					s.logger.Error("❌ Failed to get tasks for user",
						"project_id", projectID,
						"user_id", uid,
						"err", err)
					return
				}

				// Безопасно добавляем задачи
				mu.Lock()
				allWorkPackages = append(allWorkPackages, userTasks...)
				mu.Unlock()

				if len(userTasks) > 0 {
					s.logger.Debug("User tasks found",
						"project_id", projectID,
						"user_id", uid,
						"count", len(userTasks))
				}
			}(projectID, assigneeID)
		}
	}

	wg.Wait()
	s.logger.Info("All tasks collected", "total_tasks", len(allWorkPackages))
	return allWorkPackages, nil
}

func (s *OpenProjectService) getWorkPackagesForUser(projectID, assigneeID string) ([]models.WorkPackage, error) {
	baseURL := fmt.Sprintf("%s/api/v3/work_packages", s.opts.BaseURL)

	filters := fmt.Sprintf(`[
        {"status": {"operator": "!", "values": ["12", "10", "14", "8"]}},
        {"project": {"operator": "=", "values": ["%s"]}},
        {"assignee": {"operator": "=", "values": ["%s"]}}
    ]`, projectID, assigneeID)

	params := url.Values{}
	params.Add("filters", filters)
	params.Add("pageSize", "100")

	fullURL := baseURL + "?" + params.Encode()

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}

	auth := "apikey:" + s.opts.ApiToken
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	req.Header.Set("Authorization", basicAuth)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result models.WorkPackageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Embedded.Elements, nil
}

// extractIDFromHref извлекает из пути (href) идентификатор
func extractIDFromHref(href string) string {
	parts := strings.Split(href, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// GenerateExcelReport создает Excel файл с двумя листами
func (s *OpenProjectService) GenerateExcelReport() (string, error) {
	// Получаем задачи по всем пользователям
	workPackages, err := s.GetWorkPackagesByUsers()
	if err != nil {
		return "", fmt.Errorf("ошибка получения задач: %w", err)
	}

	// Проверим, есть ли задача 4600 (ДЛЯ ТЕСТА)
	found := false
	for _, wp := range workPackages {
		if wp.ID == 4600 {
			fmt.Printf("✅ ЗАДАЧА 4600 ТЕПЕРЬ В ВЫГРУЗКЕ! Статус: %s\n", wp.Links.Status.Title)
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("❌ ЗАДАЧА 4600 ВСЕ ЕЩЕ НЕ В ВЫГРУЗКЕ\n")
	}

	s.logger.Info("Общее количество задач в выгрузке: ", "count", len(workPackages))

	// Фильтруем только ошибки
	errorTasks := s.filterErrorTasks(workPackages)

	// Собираем статистику по сотрудникам
	employeeStats := s.calculateEmployeeStats(workPackages)

	s.logger.Info("Creating Excel file", "total_tasks", len(workPackages), "error_tasks", len(errorTasks), "employees", len(employeeStats))

	// Создаем Excel файл
	return s.createExcelFile(errorTasks, employeeStats)
}

// filterErrorTasks фильтрует только задачи типа "Ошибка"
func (s *OpenProjectService) filterErrorTasks(tasks []models.WorkPackage) []models.WorkPackage {
	var errorTasks []models.WorkPackage
	for _, task := range tasks {
		if task.ID == 4600 {
			s.logger.Info("Filtered task with ID 4600", "task", task)
		}
		taskType := extractIDFromHref(task.Links.Type.Href)
		if taskType == "7" {
			errorTasks = append(errorTasks, task)

		}
		//if task.Links.Type.Title == "Ошибка" {
		//	errorTasks = append(errorTasks, task)
		//}
	}
	return errorTasks
}

// calculateEmployeeStats рассчитывает статистику по сотрудникам
func (s *OpenProjectService) calculateEmployeeStats(tasks []models.WorkPackage) []models.EmployeeStats {
	statsMap := make(map[string]*models.EmployeeStats)
	today := time.Now().Format("2006-01-02")

	for _, task := range tasks {
		assignee := task.Links.Assignee.Title
		if assignee == "" {
			continue
		}

		if _, exists := statsMap[assignee]; !exists {
			statsMap[assignee] = &models.EmployeeStats{Name: assignee}
		}

		stats := statsMap[assignee]

		// Задачи в работе
		if s.isInProgressStatus(task.Links.Status.Title) {
			stats.InProgress++
		}

		// Задачи, переданные на тест сегодня
		updatedDate := strings.Split(task.UpdatedAt, "T")[0] // Берем только дату из "2024-12-19T10:30:00Z"
		if s.isSentToTestStatus(task.Links.Status.Title) && updatedDate == today {
			stats.SentToTestToday++
		}

		// Бэклог
		if s.isBacklogStatus(task.Links.Status.Title) {
			stats.Backlog++
		}
	}

	var stats []models.EmployeeStats
	for _, stat := range statsMap {
		stats = append(stats, *stat)
	}

	return stats
}

// Вспомогательные методы для определения статусов
func (s *OpenProjectService) isInProgressStatus(status string) bool {
	inProgressStatuses := []string{"в работе", "in progress", "выполняется"}
	return s.containsStatus(status, inProgressStatuses)
}

func (s *OpenProjectService) isSentToTestStatus(status string) bool {
	testStatuses := []string{"готово к тесту", "тестирование", "на тесте"}
	return s.containsStatus(status, testStatuses)
}

func (s *OpenProjectService) isBacklogStatus(status string) bool {
	backlogStatuses := []string{"новое", "new", "ожидание", "требует уточнения"}
	return s.containsStatus(status, backlogStatuses)
}

func (s *OpenProjectService) containsStatus(status string, statusList []string) bool {
	lowerStatus := strings.ToLower(status)
	for _, s := range statusList {
		if strings.Contains(lowerStatus, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// createExcelFile создает Excel файл с двумя листами
func (s *OpenProjectService) createExcelFile(errorTasks []models.WorkPackage, employeeStats []models.EmployeeStats) (string, error) {
	f := excelize.NewFile()

	// Удаляем дефолтный лист
	f.DeleteSheet("Sheet1")

	// Создаем лист "Ошибки"
	f.NewSheet("Ошибки")

	// Заголовки для листа "Ошибки"
	headers := []string{
		"ID", "Тема", "Тип", "Статус", "Назначенный",
		"Ответственный", "Проект", "Дата начала", "Дата окончания",
	}

	// Устанавливаем заголовки
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Ошибки", cell, header)
	}

	// Заполняем данные ошибок
	for row, task := range errorTasks {
		rowNum := row + 2

		f.SetCellValue("Ошибки", fmt.Sprintf("A%d", rowNum), task.ID)
		f.SetCellValue("Ошибки", fmt.Sprintf("B%d", rowNum), task.Subject)
		f.SetCellValue("Ошибки", fmt.Sprintf("C%d", rowNum), task.Links.Type.Title)
		f.SetCellValue("Ошибки", fmt.Sprintf("D%d", rowNum), task.Links.Status.Title)
		f.SetCellValue("Ошибки", fmt.Sprintf("E%d", rowNum), task.Links.Assignee.Title)
		f.SetCellValue("Ошибки", fmt.Sprintf("F%d", rowNum), task.Links.Responsible.Title)
		f.SetCellValue("Ошибки", fmt.Sprintf("G%d", rowNum), task.Links.Project.Title)

		if task.StartDate != nil {
			f.SetCellValue("Ошибки", fmt.Sprintf("H%d", rowNum), formatDateForExcel(*task.StartDate))
		}

		if task.DueDate != nil {
			f.SetCellValue("Ошибки", fmt.Sprintf("I%d", rowNum), formatDateForExcel(*task.DueDate))
		}
	}

	// Автоматическая ширина колонок для листа "Ошибки"
	for i := range headers {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth("Ошибки", colName, colName, 20)
	}

	// Создаем лист "ФИО"
	employeeSheetIndex, _ := f.NewSheet("ФИО")

	// Заголовки для листа "ФИО"
	employeeHeaders := []string{
		"ФИО", "В работе", "Передано на тесты сегодня", "Бэклог",
	}

	// Устанавливаем заголовки
	for i, header := range employeeHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("ФИО", cell, header)
	}

	// Заполняем статистику сотрудников
	for row, stat := range employeeStats {
		rowNum := row + 2

		f.SetCellValue("ФИО", fmt.Sprintf("A%d", rowNum), stat.Name)
		f.SetCellValue("ФИО", fmt.Sprintf("B%d", rowNum), stat.InProgress)
		f.SetCellValue("ФИО", fmt.Sprintf("C%d", rowNum), stat.SentToTestToday)
		f.SetCellValue("ФИО", fmt.Sprintf("D%d", rowNum), stat.Backlog)
	}

	// Автоматическая ширина колонок для листа "ФИО"
	for i := range employeeHeaders {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth("ФИО", colName, colName, 25)
	}

	// Устанавливаем активным лист "ФИО"
	f.SetActiveSheet(employeeSheetIndex)

	// Создаем имя файла с timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	fileName := fmt.Sprintf("5921_%s.xlsx", timestamp)
	filePath := filepath.Join(s.opts.SaveDir, fileName)

	if err := os.MkdirAll(s.opts.SaveDir, 0755); err != nil {
		return "", fmt.Errorf("error to make directory: %w", err)
	}

	// Сохраняем файл
	if err := f.SaveAs(filePath); err != nil {
		return "", fmt.Errorf("error to save file: %w", err)
	}

	s.logger.Info("Excel report created successfully",
		"file_path", filePath,
		"error_tasks", len(errorTasks),
		"employee_stats", len(employeeStats))
	// Сохраняем файл
	return filePath, nil
}

// parseDate парсит строку даты в формате "2006-01-02"
func parseDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("пустая дата")
	}
	return time.Parse("2006-01-02", dateStr)
}

// formatDateForExcel форматирует дату для Excel
func formatDateForExcel(dateStr string) string {
	if dateStr == "" {
		return ""
	}
	t, err := parseDate(dateStr)
	if err != nil {
		return dateStr
	}
	return t.Format("02.01.2006")
}
