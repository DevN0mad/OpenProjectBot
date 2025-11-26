package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenProjectService основной сервис для работы с OpenProject
type OpenProjectService struct {
	baseURL     string   `yaml:"baseURL" validate:"required"`
	apiToken    string   `yaml:"apiToken" validate:"required"`
	projectIDs  []string `yaml:"projectIDs" validate:"required"`
	assigneeIDs []string `yaml:"assigneeIDs" validate:"required"`
	client      *http.Client
}

// WorkPackage представляет задачу в OpenProject
type WorkPackage struct {
	ID      int    `json:"id"`
	Subject string `json:"subject"`
	Links   struct {
		Type struct {
			Title string `json:"title"`
			Href  string `json:"href"` // Добавляем href для получения ID
		} `json:"type"`
		Status struct {
			Title string `json:"title"`
		} `json:"status"`
		Assignee struct {
			Title string `json:"title"`
			Href  string `json:"href"` // Добавляем href
		} `json:"assignee"`
		Responsible struct {
			Title string `json:"title"`
		} `json:"responsible"`
		Project struct {
			Title string `json:"title"`
			Href  string `json:"href"` // Добавляем href
		} `json:"project"`
	} `json:"_links"`
	StartDate *string `json:"startDate"`
	DueDate   *string `json:"dueDate"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
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
func Init(baseURL, apiToken string, projectIDs, assigneeIDs []string) *OpenProjectService {
	return &OpenProjectService{
		baseURL:     baseURL,
		apiToken:    apiToken,
		projectIDs:  projectIDs,
		assigneeIDs: assigneeIDs,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

// GetWorkPackages получает все задачи проекта через Basic Auth
func (s *OpenProjectService) GetWorkPackages() ([]WorkPackage, error) {
	var allWorkPackages []WorkPackage

	for _, projectID := range s.projectIDs {
		workPackages, err := s.getWorkPackagesForProject(projectID)
		if err != nil {
			return nil, fmt.Errorf("ошибка получения задач для проекта %s: %w", projectID, err)
		}

		allWorkPackages = append(allWorkPackages, workPackages...)
	}

	return allWorkPackages, nil
}

// getWorkPackagesForProject получает все задачи для конкретного проекта
func (s *OpenProjectService) getWorkPackagesForProject(projectID string) ([]WorkPackage, error) {
	// Рассчитываем временной диапазон: с 17:00 прошлого дня до 17:00 текущего дня
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.Local
	}
	now := time.Now().In(location)

	// Сегодня в 17:00 (время работы бота)
	today17 := time.Date(now.Year(), now.Month(), now.Day(), 17, 0, 0, 0, location)

	// Вчера в 17:00
	yesterday := now.AddDate(0, 0, -1)
	yesterday17 := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 17, 0, 0, 0, location)

	// Форматируем даты в формат для OP
	startDate := yesterday17.Format("2006-01-02T15:04:05Z")
	endDate := today17.Format("2006-01-02T15:04:05Z")

	baseURL := fmt.Sprintf("%s/api/v3/projects/%s/work_packages", s.baseURL, projectID)

	// Только фильтр по дате
	filters := fmt.Sprintf(
		`[{"updatedAt":{"operator":"<>d","values":["%s","%s"]}}]`,
		startDate, endDate,
	)

	params := url.Values{}
	params.Add("filters", filters)
	fullURL := baseURL + "?" + params.Encode()

	fmt.Printf("URL запроса: %s\n", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	// Basic Auth: username = "apikey", password = API токен
	auth := "apikey:" + s.apiToken
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	req.Header.Set("Authorization", basicAuth)
	req.Header.Set("Content-Type", "application/json")

	// Логируем период для отладки
	fmt.Printf("Запрос задач за период: %s - %s\n",
		yesterday17.Format("2006-01-02 15:04"),
		today17.Format("2006-01-02 15:04"))

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

	// Фильтруем по исполнителю на стороне Go
	filteredPackages := s.filterByAssignees(result.Embedded.Elements, s.assigneeIDs)

	fmt.Printf("Получено задач: %d (после фильтрации: %d)\n",
		len(result.Embedded.Elements), len(filteredPackages))

	return filteredPackages, nil
}

// filterByAssignees фильтрует исполнителей по assigneeIDs
func (s *OpenProjectService) filterByAssignees(workPackages []WorkPackage, assigneeIDs []string) []WorkPackage {
	assigneeMap := make(map[string]bool)
	for _, id := range assigneeIDs {
		assigneeMap[id] = true
	}

	var filtered []WorkPackage
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
func (s *OpenProjectService) GenerateExcelReport(filePath string) error {
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

	jsonErrorTasks, err := json.MarshalIndent(errorTasks, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка форматирования JSON: %w", err)
	}

	fmt.Printf("ERROR TASKS (%d):\n%s\n", len(errorTasks), string(jsonErrorTasks))

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
