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

// GetWorkPackagesByUsers получает задачи по всем пользователям и проектам
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

	// Фильтруем задачи, которые не закрыты (статус не равен 12)
	filters := fmt.Sprintf(`[
        {"status": {"operator": "!", "values": ["8", "12", "19"]}},
        {"project": {"operator": "=", "values": ["%s"]}},
        {"assignee": {"operator": "=", "values": ["%s"]}}
    ]`, projectID, assigneeID)

	params := url.Values{}
	params.Add("filters", filters)
	params.Add("pageSize", "500")

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

// GenerateExcelReport создает Excel файл с тремя листами
func (s *OpenProjectService) GenerateExcelReport() (string, error) {
	// Получаем задачи по всем пользователям
	workPackages, err := s.GetWorkPackagesByUsers()
	if err != nil {
		return "", fmt.Errorf("ошибка получения задач: %w", err)
	}

	s.logger.Info("Общее количество активных задач в выгрузке: ", "count", len(workPackages))

	// Фильтруем задачи по трем категориям
	backlogTasks := s.filterBacklogTasks(workPackages)
	inProgressTasks := s.filterInProgressTasks(workPackages)
	readyForTestTasks := s.filterReadyForTestTasks(workPackages)

	// Создаем сводную статистику
	summaryStats := s.calculateSummaryStats(backlogTasks, inProgressTasks, readyForTestTasks)

	s.logger.Info("Creating Excel file",
		"backlog_tasks", len(backlogTasks),
		"in_progress_tasks", len(inProgressTasks),
		"ready_for_test_tasks", len(readyForTestTasks),
		"employees", len(summaryStats))

	// Создаем Excel файл
	return s.createExcelFile(backlogTasks, inProgressTasks, readyForTestTasks, summaryStats)
}

// filterBacklogTasks фильтрует задачи для бэклога (активные, кроме "Готово к тесту")
func (s *OpenProjectService) filterBacklogTasks(tasks []models.WorkPackage) []models.WorkPackage {
	var backlogTasks []models.WorkPackage
	for _, task := range tasks {
		status := strings.ToLower(task.Links.Status.Title)
		statusId := extractIDFromHref(task.Links.Status.Href)
		// Исключаем статусы "Готово к тесту" и его варианты
		if !s.containsStatus(status, []string{"готово к тесту", "готово к тестированию", "ready for test"}) && statusId != "7" {
			backlogTasks = append(backlogTasks, task)
		}
	}
	return backlogTasks
}

// filterInProgressTasks фильтрует задачи "В процессе"
func (s *OpenProjectService) filterInProgressTasks(tasks []models.WorkPackage) []models.WorkPackage {
	var inProgressTasks []models.WorkPackage
	for _, task := range tasks {
		status := strings.ToLower(task.Links.Status.Title)
		if s.containsStatus(status, []string{"в процессе", "в работе", "in progress", "выполняется"}) {
			inProgressTasks = append(inProgressTasks, task)
		}
	}
	return inProgressTasks
}

// filterReadyForTestTasks фильтрует задачи "Готово к тесту" с сегодняшней датой передачи
func (s *OpenProjectService) filterReadyForTestTasks(tasks []models.WorkPackage) []models.WorkPackage {
	var readyForTestTasks []models.WorkPackage
	today := time.Now().Format("2006-01-02")

	for _, task := range tasks {
		status := strings.ToLower(task.Links.Status.Title)
		if s.containsStatus(status, []string{"готово к тесту", "готово к тестированию", "ready for test"}) {
			// Проверяем кастомное поле "Дата передачи на тестирование"
			// Предполагаем, что это поле доступно через task.CustomFields или аналогичное поле
			testDate := s.getTestingTransferDate(task)
			if testDate == today {
				readyForTestTasks = append(readyForTestTasks, task)
			}
		}
	}
	return readyForTestTasks
}

// getTestingTransferDate получает дату передачи на тестирование из кастомных полей
// Вам нужно адаптировать этот метод под структуру ваших кастомных полей в OpenProject
func (s *OpenProjectService) getTestingTransferDate(task models.WorkPackage) string {
	// Пример реализации - вам нужно настроить под вашу структуру данных
	// Обычно кастомные поля находятся в task.CustomFields или аналогичной структуре

	// Временная реализация - используем UpdatedAt как пример
	// Замените на реальное поле из вашей структуры
	if task.UpdatedAt != "" {
		return strings.Split(task.UpdatedAt, "T")[0]
	}
	return ""
}

// EmployeeSummary статистика по сотрудникам для сводной таблицы
type EmployeeSummary struct {
	Name              string
	InProgressCount   int
	ReadyForTestCount int
	BacklogCount      int
}

// calculateSummaryStats рассчитывает сводную статистику по сотрудникам
func (s *OpenProjectService) calculateSummaryStats(backlogTasks, inProgressTasks, readyForTestTasks []models.WorkPackage) []EmployeeSummary {
	statsMap := make(map[string]*EmployeeSummary)

	// Считаем задачи бэклога
	for _, task := range backlogTasks {
		assignee := task.Links.Assignee.Title
		if assignee == "" {
			continue
		}
		if _, exists := statsMap[assignee]; !exists {
			statsMap[assignee] = &EmployeeSummary{Name: assignee}
		}
		statsMap[assignee].BacklogCount++
	}

	// Считаем задачи в работе
	for _, task := range inProgressTasks {
		assignee := task.Links.Assignee.Title
		if assignee == "" {
			continue
		}
		if _, exists := statsMap[assignee]; !exists {
			statsMap[assignee] = &EmployeeSummary{Name: assignee}
		}
		statsMap[assignee].InProgressCount++
	}

	// Считаем задачи готовые к тесту
	for _, task := range readyForTestTasks {
		assignee := task.Links.Assignee.Title
		if assignee == "" {
			continue
		}
		if _, exists := statsMap[assignee]; !exists {
			statsMap[assignee] = &EmployeeSummary{Name: assignee}
		}
		statsMap[assignee].ReadyForTestCount++
	}

	var summary []EmployeeSummary
	for _, stat := range statsMap {
		summary = append(summary, *stat)
	}

	return summary
}

// containsStatus проверяет содержит ли статус нужные ключевые слова
func (s *OpenProjectService) containsStatus(status string, statusList []string) bool {
	for _, s := range statusList {
		if strings.Contains(status, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// createExcelFile создает Excel файл с тремя листами
func (s *OpenProjectService) createExcelFile(backlogTasks, inProgressTasks, readyForTestTasks []models.WorkPackage, summaryStats []EmployeeSummary) (string, error) {
	f := excelize.NewFile()

	// 1. Лист "Бэклог"
	s.createBacklogSheet(f, backlogTasks)

	// Удаляем дефолтный лист
	f.DeleteSheet("Sheet1")

	// 2. Лист "Активные задачи"
	s.createActiveTasksSheet(f, inProgressTasks, readyForTestTasks)

	// 3. Лист "Сводная"
	s.createSummarySheet(f, summaryStats)

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
		"backlog_tasks", len(backlogTasks),
		"in_progress_tasks", len(inProgressTasks),
		"ready_for_test_tasks", len(readyForTestTasks))

	return filePath, nil
}

// createBacklogSheet создает лист с задачами бэклога
func (s *OpenProjectService) createBacklogSheet(f *excelize.File, tasks []models.WorkPackage) {
	f.NewSheet("Бэклог")

	headers := []string{
		"ID", "Тема", "Тип", "Статус", "Назначенный",
		"Ответственный", "Проект", "Дата создания", "Дата окончания", "Дата обновления",
	}

	// Устанавливаем заголовки
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Бэклог", cell, header)
	}

	// Заполняем данные
	for row, task := range tasks {
		rowNum := row + 2
		f.SetCellValue("Бэклог", fmt.Sprintf("A%d", rowNum), task.ID)
		f.SetCellValue("Бэклог", fmt.Sprintf("B%d", rowNum), task.Subject)
		f.SetCellValue("Бэклог", fmt.Sprintf("C%d", rowNum), task.Links.Type.Title)
		f.SetCellValue("Бэклог", fmt.Sprintf("D%d", rowNum), task.Links.Status.Title)
		f.SetCellValue("Бэклог", fmt.Sprintf("E%d", rowNum), task.Links.Assignee.Title)
		f.SetCellValue("Бэклог", fmt.Sprintf("F%d", rowNum), task.Links.Responsible.Title)
		f.SetCellValue("Бэклог", fmt.Sprintf("G%d", rowNum), task.Links.Project.Title)
		//f.SetCellValue("Бэклог", fmt.Sprintf("H%d", rowNum), formatDateForExcel(strings.Split(task.CreatedAt, "T")[0]))
		//f.SetCellValue("Бэклог", fmt.Sprintf("I%d", rowNum), formatDateForExcel(strings.Split(task.UpdatedAt, "T")[0]))

		// Дата создания (берем только дату из timestamp)
		createdDate := s.extractDateOnly(task.CreatedAt)
		f.SetCellValue("Бэклог", fmt.Sprintf("H%d", rowNum), createdDate)

		// Дата окончания (может быть пустой)
		var dueDate string
		if task.DueDate != nil {
			dueDate = formatDateForExcel(*task.DueDate)
		}
		f.SetCellValue("Бэклог", fmt.Sprintf("I%d", rowNum), dueDate)

		// Дата обновления (берем только дату из timestamp)
		updatedDate := s.extractDateOnly(task.UpdatedAt)
		f.SetCellValue("Бэклог", fmt.Sprintf("J%d", rowNum), updatedDate)
	}

	// Автоматическая ширина колонок
	for i := range headers {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth("Бэклог", colName, colName, 20)
	}
}

// extractDateOnly извлекает только дату из строки формата ISO
func (s *OpenProjectService) extractDateOnly(isoString string) string {
	if isoString == "" {
		return ""
	}

	// Разделяем по "T" чтобы получить только дату
	parts := strings.Split(isoString, "T")
	if len(parts) > 0 {
		return formatDateForExcel(parts[0])
	}

	return ""
}

// createActiveTasksSheet создает лист с активными задачами
func (s *OpenProjectService) createActiveTasksSheet(f *excelize.File, inProgressTasks, readyForTestTasks []models.WorkPackage) {
	f.NewSheet("Активные задачи")

	headers := []string{
		"ID", "Тема", "Тип", "Статус", "Назначенный",
		"Ответственный", "Проект",
	}

	// Устанавливаем заголовки
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Активные задачи", cell, header)
	}

	rowNum := 2

	// Добавляем задачи "В процессе"
	for _, task := range inProgressTasks {
		f.SetCellValue("Активные задачи", fmt.Sprintf("A%d", rowNum), task.ID)
		f.SetCellValue("Активные задачи", fmt.Sprintf("B%d", rowNum), task.Subject)
		f.SetCellValue("Активные задачи", fmt.Sprintf("C%d", rowNum), task.Links.Type.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("D%d", rowNum), task.Links.Status.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("E%d", rowNum), task.Links.Assignee.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("F%d", rowNum), task.Links.Responsible.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("G%d", rowNum), task.Links.Project.Title)
		//f.SetCellValue("Активные задачи", fmt.Sprintf("H%d", rowNum), "") // Дата передачи на тест не применима
		//f.SetCellValue("Активные задачи", fmt.Sprintf("I%d", rowNum), "В процессе")
		rowNum++
	}

	// Добавляем задачи "Готово к тесту"
	for _, task := range readyForTestTasks {
		f.SetCellValue("Активные задачи", fmt.Sprintf("A%d", rowNum), task.ID)
		f.SetCellValue("Активные задачи", fmt.Sprintf("B%d", rowNum), task.Subject)
		f.SetCellValue("Активные задачи", fmt.Sprintf("C%d", rowNum), task.Links.Type.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("D%d", rowNum), task.Links.Status.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("E%d", rowNum), task.Links.Assignee.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("F%d", rowNum), task.Links.Responsible.Title)
		f.SetCellValue("Активные задачи", fmt.Sprintf("G%d", rowNum), task.Links.Project.Title)
		//f.SetCellValue("Активные задачи", fmt.Sprintf("H%d", rowNum), s.getTestingTransferDate(task))
		//f.SetCellValue("Активные задачи", fmt.Sprintf("I%d", rowNum), "Готово к тесту")
		rowNum++
	}

	// Автоматическая ширина колонок
	for i := range headers {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth("Активные задачи", colName, colName, 20)
	}
}

// createSummarySheet создает сводный лист
func (s *OpenProjectService) createSummarySheet(f *excelize.File, summaryStats []EmployeeSummary) {
	summarySheetIndex, _ := f.NewSheet("Сводная")

	headers := []string{
		"Сотрудник", "В работе", "Передано на тесты сегодня", "Бэклог",
	}

	// Устанавливаем заголовки
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Сводная", cell, header)
	}

	// Заполняем статистику
	for row, stat := range summaryStats {
		rowNum := row + 2
		f.SetCellValue("Сводная", fmt.Sprintf("A%d", rowNum), stat.Name)
		f.SetCellValue("Сводная", fmt.Sprintf("B%d", rowNum), stat.InProgressCount)
		f.SetCellValue("Сводная", fmt.Sprintf("C%d", rowNum), stat.ReadyForTestCount)
		f.SetCellValue("Сводная", fmt.Sprintf("D%d", rowNum), stat.BacklogCount)
	}

	// Автоматическая ширина колонок
	for i := range headers {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth("Сводная", colName, colName, 25)
	}

	// Устанавливаем активным лист "Сводная"
	f.SetActiveSheet(summarySheetIndex)
}

// formatDateForExcel форматирует дату для Excel
func formatDateForExcel(dateStr string) string {
	if dateStr == "" {
		return ""
	}
	// Пытаемся распарсить дату в формате "2006-01-02"
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr // Возвращаем как есть, если не удалось распарсить
	}
	return t.Format("02.01.2006")
}

// extractIDFromHref извлекает ID из ссылки
func extractIDFromHref(href string) string {
	parts := strings.Split(href, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
