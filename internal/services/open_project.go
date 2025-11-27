package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DevN0mad/OpenProjectBot/internal/models"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// OpenProjectService основной сервис для работы с OpenProject

type OpenProjectOpts struct {
	BaseURL     string   `yaml:"baseURL" validate:"required"`
	ApiToken    string   `yaml:"apiToken" validate:"required"`
	ProjectIDs  []string `yaml:"projectIDs" validate:"required"`
	AssigneeIDs []string `yaml:"assigneeIDs" validate:"required"`
	SaveDir     string   `yaml:"saveDir" validate:"required"`
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

// GetWorkPackages получает все задачи проекта через Basic Auth
func (s *OpenProjectService) GetWorkPackages() ([]models.WorkPackage, error) {
	var allWorkPackages []models.WorkPackage

	s.logger.Info("Starting tasks export", "projects_count", len(s.opts.ProjectIDs))

	for i, projectID := range s.opts.ProjectIDs {
		s.logger.Info("Processing project", "current", i+1, "total", len(s.opts.ProjectIDs), "project_id", projectID)

		workPackages, err := s.getWorkPackagesForProject(projectID)
		if err != nil {
			s.logger.Error("❌ Failed to get tasks for project", "project_id", projectID, "error", err)
			return nil, fmt.Errorf("ошибка получения задач для проекта %s: %w", projectID, err)
		}

		allWorkPackages = append(allWorkPackages, workPackages...)
		s.logger.Info("✅ Project tasks added", "project_id", projectID, "added", len(workPackages), "total", len(allWorkPackages))
	}

	s.logger.Info("✅ Total active tasks found", "count", len(allWorkPackages))

	return allWorkPackages, nil
}

// getWorkPackagesForProject получает все задачи для конкретного проекта
func (s *OpenProjectService) getWorkPackagesForProject(projectID string) ([]models.WorkPackage, error) {
	var allWorkPackages []models.WorkPackage
	page := 1
	pageSize := 100

	s.logger.Debug("Starting pagination for project", "project_id", projectID)

	for {
		s.logger.Debug("Fetching page", "page", page)

		baseURL := fmt.Sprintf("%s/api/v3/projects/%s/work_packages", s.opts.BaseURL, projectID)

		// Фильтр: статус НЕ равен 12 (не закрыто)
		filters := `[{"status":{"operator": "!","values":["12"]}}]`

		params := url.Values{}
		params.Add("filters", filters)
		params.Add("pageSize", fmt.Sprintf("%d", pageSize))
		params.Add("offset", fmt.Sprintf("%d", (page-1)*pageSize))

		fullURL := baseURL + "?" + params.Encode()

		workPackages, total, err := s.fetchWorkPackagesPage(fullURL)
		if err != nil {
			return nil, err
		}

		s.logger.Debug("Page tasks received", "page", page, "tasks_on_page", len(workPackages), "total_in_project", total)

		allWorkPackages = append(allWorkPackages, workPackages...)

		// Проверяем, есть ли еще страницы
		if len(allWorkPackages) >= total {
			s.logger.Debug("Pagination completed for project", "project_id", projectID)
			break
		}

		// Защита от бесконечного цикла
		if page > 100 {
			s.logger.Warn("Pagination interrupted - too many pages", "max_pages", 100)
			break
		}

		page++
	}

	// Фильтруем по исполнителю на стороне Go
	filteredPackages := s.filterByAssignees(allWorkPackages, s.opts.AssigneeIDs)

	s.logger.Info("Project tasks processed", "project_id", projectID, "received", len(allWorkPackages), "after_filtering", len(filteredPackages))

	return filteredPackages, nil
}

// fetchWorkPackagesPage получает одну страницу задач
func (s *OpenProjectService) fetchWorkPackagesPage(url string) ([]models.WorkPackage, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	auth := "apikey:" + s.opts.ApiToken
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	req.Header.Set("Authorization", basicAuth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка выполнения запр: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("ошибка API %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	var result models.WorkPackageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("ошибка парсинга JSON: %w", err)
	}

	// Получаем общее количество из заголовков
	total := result.Total

	return result.Embedded.Elements, total, nil
}

// filterByAssignees фильтрует исполнителей по assigneeIDs
func (s *OpenProjectService) filterByAssignees(workPackages []models.WorkPackage, assigneeIDs []string) []models.WorkPackage {
	assigneeMap := make(map[string]bool)
	for _, id := range assigneeIDs {
		assigneeMap[id] = true
	}

	var filtered []models.WorkPackage
	for _, wp := range workPackages {
		if wp.Links.Assignee.Href != "" {
			// Извлекаем ID из href (например: "/api/v3/users/20")
			id := extractIDFromHref(wp.Links.Assignee.Href)
			if id != "" && assigneeMap[id] {
				filtered = append(filtered, wp)
			}
		}
	}
	return filtered
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
func (s *OpenProjectService) GenerateExcelReport() error {
	// Получаем задачи
	workPackages, err := s.GetWorkPackages()
	if err != nil {
		return fmt.Errorf("ошибка получения задач: %w", err)
	}

	// Красиво форматируем JSON
	//jsonData, err := json.MarshalIndent(workPackages, "", "  ")
	//if err != nil {
	//	return fmt.Errorf("ошибка форматирования JSON: %w", err)
	//}

	//fmt.Printf("WORK PACKAGES (%d):\n%s\n", len(workPackages), string(jsonData))

	// Фильтруем только ошибки
	errorTasks := s.filterErrorTasks(workPackages)
	//
	//jsonErrorTasks, err := json.MarshalIndent(errorTasks, "", "  ")
	//if err != nil {
	//	return fmt.Errorf("ошибка форматирования JSON: %w", err)
	//}
	//
	//fmt.Printf("ERROR TASKS (%d):\n%s\n", len(errorTasks), string(jsonErrorTasks))

	// Собираем статистику по сотрудникам
	employeeStats := s.calculateEmployeeStats(workPackages)

	s.logger.Info("Creating Excel file", "total_tasks", len(workPackages), "error_tasks", len(errorTasks), "employees", len(employeeStats))

	// Создаем Excel файл
	return s.createExcelFile(s.opts.SaveDir, errorTasks, employeeStats)
}

// filterErrorTasks фильтрует только задачи типа "Ошибка"
func (s *OpenProjectService) filterErrorTasks(tasks []models.WorkPackage) []models.WorkPackage {
	var errorTasks []models.WorkPackage
	for _, task := range tasks {
		if task.Links.Type.Title == "Ошибка" {
			errorTasks = append(errorTasks, task)
		}
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
func (s *OpenProjectService) createExcelFile(filePath string, errorTasks []models.WorkPackage, employeeStats []models.EmployeeStats) error {
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

	s.logger.Info("Saving Excel file", "path", filePath)
	// Сохраняем файл
	return f.SaveAs("test_report.xlsx")
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
