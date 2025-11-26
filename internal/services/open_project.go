package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenProjectService основной сервис для работы с OpenProject
type OpenProjectService struct {
	baseURL   string
	apiToken  string
	projectID string
	client    *http.Client
}

// WorkPackage представляет задачу в OpenProject
type WorkPackage struct {
	ID      int    `json:"id"`
	Subject string `json:"subject"`
	Links   struct {
		Type struct {
			Title string `json:"title"`
		} `json:"type"`
		Status struct {
			Title string `json:"title"`
		} `json:"status"`
		Assignee struct {
			Title string `json:"title"`
		} `json:"assignee"`
		Responsible struct {
			Title string `json:"title"`
		} `json:"responsible"`
		Project struct {
			Title string `json:"title"`
		} `json:"project"`
	} `json:"_links"`
	StartDate *string `json:"startDate"` // Изменено на string
	DueDate   *string `json:"dueDate"`   // Изменено на string
	CreatedAt string  `json:"createdAt"` // Изменено на string
	UpdatedAt string  `json:"updatedAt"` // Изменено на string
}

// WorkPackageResponse представляет ответ API с задачами
type WorkPackageResponse struct {
	Embedded struct {
		Elements []WorkPackage `json:"elements"`
	} `json:"_embedded"`
	Total int `json:"total"`
}

// EmployeeStats представляет статистику по сотруднику
type EmployeeStats struct {
	Name            string
	InProgress      int
	SentToTestToday int
	Backlog         int
}

// Init инициализирует сервис с API токеном
func Init(baseURL, apiToken, projectID string) *OpenProjectService {
	return &OpenProjectService{
		baseURL:   baseURL,
		apiToken:  apiToken,
		projectID: projectID,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// GetWorkPackages получает все задачи проекта через Basic Auth
func (s *OpenProjectService) GetWorkPackages() ([]WorkPackage, error) {
	url := fmt.Sprintf("%s/api/v3/projects/%s/work_packages", s.baseURL, s.projectID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	// Basic Auth: username = "apikey", password = API токен
	auth := "apikey:" + s.apiToken
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	req.Header.Set("Authorization", basicAuth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка выполнения запроса: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ошибка API %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	var result WorkPackageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w", err)
	}

	return result.Embedded.Elements, nil
}

// GenerateExcelReport создает Excel файл с двумя листами
func (s *OpenProjectService) GenerateExcelReport(filePath string) error {
	// Получаем задачи
	workPackages, err := s.GetWorkPackages()
	if err != nil {
		return fmt.Errorf("ошибка получения задач: %w", err)
	}

	// Фильтруем только ошибки
	errorTasks := s.filterErrorTasks(workPackages)

	// Собираем статистику по сотрудникам
	employeeStats := s.calculateEmployeeStats(workPackages)

	// Создаем Excel файл
	return s.createExcelFile(filePath, errorTasks, employeeStats)
}

// filterErrorTasks фильтрует только задачи типа "Ошибка"
func (s *OpenProjectService) filterErrorTasks(tasks []WorkPackage) []WorkPackage {
	var errorTasks []WorkPackage
	for _, task := range tasks {
		if task.Links.Type.Title == "Ошибка" {
			errorTasks = append(errorTasks, task)
		}
	}
	return errorTasks
}

// calculateEmployeeStats рассчитывает статистику по сотрудникам
func (s *OpenProjectService) calculateEmployeeStats(tasks []WorkPackage) []EmployeeStats {
	statsMap := make(map[string]*EmployeeStats)
	today := time.Now().Format("2006-01-02")

	for _, task := range tasks {
		assignee := task.Links.Assignee.Title
		if assignee == "" {
			continue
		}

		if _, exists := statsMap[assignee]; !exists {
			statsMap[assignee] = &EmployeeStats{Name: assignee}
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

	var stats []EmployeeStats
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
func (s *OpenProjectService) createExcelFile(filePath string, errorTasks []WorkPackage, employeeStats []EmployeeStats) error {
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

	// Сохраняем файл
	return f.SaveAs(filePath)
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
