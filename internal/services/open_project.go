package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/models"
	"github.com/xuri/excelize/v2"
)

// OpenProjectOpts – настройки сервиса OpenProject.
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

// Init инициализирует сервис.
func Init(opts OpenProjectOpts, logger *slog.Logger) *OpenProjectService {
	if logger == nil {
		logger = slog.Default()
	}

	return &OpenProjectService{
		opts:   opts,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// ===================== Публичный метод отчёта =====================

func (s *OpenProjectService) GenerateExcelReport(ctx context.Context) (string, error) {
	reportDate := time.Now()

	s.logger.Info("starting OpenProject export",
		"projects", len(s.opts.ProjectIDs),
		"assignees", len(s.opts.AssigneeIDs),
		"report_date", reportDate.Format("2006-01-02"),
	)

	backlogTasks, inProgressTasks, sentToTestTodayTasks, err := s.collectAndClassifyTasks(ctx, reportDate)
	if err != nil {
		return "", fmt.Errorf("collect tasks: %w", err)
	}

	stats := s.calculateEmployeeStats(backlogTasks, inProgressTasks, sentToTestTodayTasks)

	s.logger.Info("tasks classified",
		"backlog", len(backlogTasks),
		"in_progress", len(inProgressTasks),
		"sent_to_test_today", len(sentToTestTodayTasks),
		"employees", len(stats),
	)

	filePath, err := s.createExcelFile(backlogTasks, inProgressTasks, sentToTestTodayTasks, stats, reportDate)
	if err != nil {
		return "", fmt.Errorf("create excel file: %w", err)
	}

	s.logger.Info("excel report created", "file_path", filePath)
	return filePath, nil
}

// ===================== Worker pool + классификация =====================

const (
	defaultWorkers  = 8
	defaultPageSize = 200

	taskCategoryBacklog         = "backlog"
	taskCategoryInProgress      = "in_progress"
	taskCategorySentToTestToday = "sent_to_test_today"
)

type opFilter struct {
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type opFilterWrapper map[string]opFilter

func (s *OpenProjectService) collectAndClassifyTasks(
	ctx context.Context,
	reportDate time.Time,
) (backlog, inProgress, sentToTestToday []models.WorkPackage, err error) {
	type job struct {
		ProjectID  string
		AssigneeID string
	}

	type result struct {
		Backlog         []models.WorkPackage
		InProgress      []models.WorkPackage
		SentToTestToday []models.WorkPackage
	}

	jobsCh := make(chan job)
	resultsCh := make(chan result)

	totalJobs := len(s.opts.ProjectIDs) * len(s.opts.AssigneeIDs)
	if totalJobs == 0 {
		return nil, nil, nil, fmt.Errorf("no projects or assignees configured")
	}

	workerCount := defaultWorkers
	if totalJobs < workerCount {
		workerCount = totalJobs
	}

	var wg sync.WaitGroup

	// workers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			for j := range jobsCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				tasks, err := s.fetchWorkPackagesForUser(ctx, j.ProjectID, j.AssigneeID)
				if err != nil {
					s.logger.Error("failed to fetch work packages",
						"worker", workerID,
						"project_id", j.ProjectID,
						"assignee_id", j.AssigneeID,
						"err", err,
					)
					continue
				}

				var res result
				for _, t := range tasks {
					category := s.classifyTask(t, reportDate)
					switch category {
					case taskCategoryBacklog:
						res.Backlog = append(res.Backlog, t)
					case taskCategoryInProgress:
						res.InProgress = append(res.InProgress, t)
					case taskCategorySentToTestToday:
						res.SentToTestToday = append(res.SentToTestToday, t)
					default:
					}
				}

				if len(res.Backlog) == 0 && len(res.InProgress) == 0 && len(res.SentToTestToday) == 0 {
					continue
				}

				select {
				case <-ctx.Done():
					return
				case resultsCh <- res:
				}
			}
		}(i + 1)
	}

	// producer
	go func() {
		defer close(jobsCh)

		for _, pid := range s.opts.ProjectIDs {
			for _, uid := range s.opts.AssigneeIDs {
				select {
				case <-ctx.Done():
					return
				case jobsCh <- job{ProjectID: pid, AssigneeID: uid}:
				}
			}
		}
	}()

	// closer для resultsCh
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var allBacklog, allInProgress, allSent []models.WorkPackage

	for r := range resultsCh {
		allBacklog = append(allBacklog, r.Backlog...)
		allInProgress = append(allInProgress, r.InProgress...)
		allSent = append(allSent, r.SentToTestToday...)
	}

	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}

	return allBacklog, allInProgress, allSent, nil
}

// ===================== Запросы в OpenProject с пагинацией =====================

func (s *OpenProjectService) fetchWorkPackagesForUser(
	ctx context.Context,
	projectID, assigneeID string,
) ([]models.WorkPackage, error) {
	// только открытые задачи: operator "o"
	filters := []opFilterWrapper{
		{"status": {Operator: "o", Values: []string{}}},
		{"project": {Operator: "=", Values: []string{projectID}}},
		{"assignee": {Operator: "=", Values: []string{assigneeID}}},
	}

	filtersJSON, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("marshal filters: %w", err)
	}

	baseURL := strings.TrimRight(s.opts.BaseURL, "/") + "/api/v3/work_packages"

	params := url.Values{}
	params.Set("filters", string(filtersJSON))
	params.Set("pageSize", strconv.Itoa(defaultPageSize))

	nextURL := baseURL + "?" + params.Encode()

	var all []models.WorkPackage

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		auth := "apikey:" + s.opts.ApiToken
		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
		req.Header.Set("Authorization", basicAuth)
		req.Header.Set("Accept", "application/hal+json")

		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("do request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("openproject api error: status=%d body=%s", resp.StatusCode, string(body))
		}

		var collection models.WorkPackageResponse
		if err := json.Unmarshal(body, &collection); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		all = append(all, collection.Embedded.Elements...)

		if collection.Links.Next == nil || collection.Links.Next.Href == "" {
			break
		}

		nextURL = s.resolveURL(collection.Links.Next.Href)
	}

	return all, nil
}

func (s *OpenProjectService) resolveURL(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return strings.TrimRight(s.opts.BaseURL, "/") + href
}

// ===================== Классификация задач =====================

func (s *OpenProjectService) classifyTask(task models.WorkPackage, reportDate time.Time) string {
	statusTitle := task.Links.Status.Title
	if statusTitle == "" {
		return ""
	}

	// 1) Все тестовые статусы ("готово к тесту", "на тесте" и т.п.)
	//    На отчёт попадает только "передано на тесты сегодня"
	if s.isSentToTestStatus(statusTitle) {
		if s.isSentToTestToday(task, reportDate) {
			return taskCategorySentToTestToday // уйдёт на 2-й лист "В работе"
		}
		// не сегодня → вообще не попадает ни на один лист
		return ""
	}

	// 2) "В процессе" — только на 2-й лист "В работе"
	if s.isInProgressStatus(statusTitle) {
		return taskCategoryInProgress
	}

	// 3) Статусы, которые нельзя показывать в бэклоге (Разработан, В ветку разработки)
	if s.isExcludedFromBacklogStatus(statusTitle) {
		return ""
	}

	// 4) Всё остальное — бэклог (1-й лист)
	return taskCategoryBacklog
}

// статусы "в процессе"
func (s *OpenProjectService) isInProgressStatus(status string) bool {
	inProgressStatuses := []string{
		"в процессе",
		"в работе",
		"in progress",
		"выполняется",
	}
	return s.containsStatus(status, inProgressStatuses)
}

// статусы "на тесте / передано на тесты"
func (s *OpenProjectService) isSentToTestStatus(status string) bool {
	testStatuses := []string{
		"готово к тесту",
		"тестирование",
		"на тесте",
		"передано на тесты",
	}
	return s.containsStatus(status, testStatuses)
}

// статусы, которые нужно исключить с бэклога
func (s *OpenProjectService) isExcludedFromBacklogStatus(status string) bool {
	excluded := []string{
		"разработан",
		"в ветку разработки",
	}
	return s.containsStatus(status, excluded)
}

func (s *OpenProjectService) containsStatus(status string, statusList []string) bool {
	lowerStatus := strings.ToLower(status)
	for _, v := range statusList {
		if strings.Contains(lowerStatus, strings.ToLower(v)) {
			return true
		}
	}
	return false
}

// "передано на тесты" именно сегодня
func (s *OpenProjectService) isSentToTestToday(task models.WorkPackage, reportDate time.Time) bool {
	if task.UpdatedAt == "" {
		return false
	}
	if !s.isSentToTestStatus(task.Links.Status.Title) {
		return false
	}

	const layout = "2006-01-02"
	targetDate := reportDate.Format(layout)

	// пробуем RFC3339
	if t, err := time.Parse(time.RFC3339, task.UpdatedAt); err == nil {
		return t.In(reportDate.Location()).Format(layout) == targetDate
	}

	// fallback по строке
	if idx := strings.Index(task.UpdatedAt, "T"); idx > 0 {
		return task.UpdatedAt[:idx] == targetDate
	}
	if len(task.UpdatedAt) >= len(layout) {
		return task.UpdatedAt[:len(layout)] == targetDate
	}
	return false
}

// ===================== Сводная статистика =====================

func (s *OpenProjectService) calculateEmployeeStats(
	backlog, inProgress, sentToTestToday []models.WorkPackage,
) []models.EmployeeStats {
	statsMap := make(map[string]*models.EmployeeStats)

	ensure := func(name string) *models.EmployeeStats {
		if name == "" {
			return nil
		}
		if _, ok := statsMap[name]; !ok {
			statsMap[name] = &models.EmployeeStats{Name: name}
		}
		return statsMap[name]
	}

	for _, t := range inProgress {
		if s := ensure(t.Links.Assignee.Title); s != nil {
			s.InProgress++
		}
	}

	for _, t := range sentToTestToday {
		if s := ensure(t.Links.Assignee.Title); s != nil {
			s.SentToTestToday++
		}
	}

	for _, t := range backlog {
		if s := ensure(t.Links.Assignee.Title); s != nil {
			s.Backlog++
		}
	}

	res := make([]models.EmployeeStats, 0, len(statsMap))
	for _, v := range statsMap {
		res = append(res, *v)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res
}

// ===================== Excel =====================

func (s *OpenProjectService) createExcelFile(
	backlogTasks, inProgressTasks, sentToTestTodayTasks []models.WorkPackage,
	employeeStats []models.EmployeeStats,
	reportDate time.Time,
) (string, error) {
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			s.logger.Error("failed to close excel file", "error", err)
		}
	}()

	headers := []string{
		"ID", "Тема", "Тип", "Статус", "Назначенный",
		"Ответственный", "Проект", "Дата начала", "Дата окончания",
	}

	// 1. Лист "Бэклог" – переименовываем дефолтный Sheet1
	const backlogSheet = "Бэклог"
	if err := f.SetSheetName("Sheet1", backlogSheet); err != nil {
		return "", fmt.Errorf("rename default sheet to backlog: %w", err)
	}
	if err := s.writeTasksSheet(f, backlogSheet, headers, backlogTasks); err != nil {
		return "", fmt.Errorf("fill backlog sheet: %w", err)
	}

	// 2. Лист "В работе"
	const workSheet = "В работе"
	if _, err := f.NewSheet(workSheet); err != nil {
		return "", fmt.Errorf("create work sheet: %w", err)
	}
	var workTasks []models.WorkPackage
	workTasks = append(workTasks, inProgressTasks...)
	workTasks = append(workTasks, sentToTestTodayTasks...)
	if err := s.writeTasksSheet(f, workSheet, headers, workTasks); err != nil {
		return "", fmt.Errorf("fill work sheet: %w", err)
	}

	// 3. Лист "Сводная"
	const summarySheet = "Сводная"
	summaryIdx, err := f.NewSheet(summarySheet)
	if err != nil {
		return "", fmt.Errorf("create summary sheet: %w", err)
	}

	summaryHeaders := []string{
		"ФИО", "В работе", "Передано на тесты сегодня", "Бэклог",
	}

	for i, h := range summaryHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(summarySheet, cell, h); err != nil {
			return "", err
		}
	}

	for row, stat := range employeeStats {
		rowNum := row + 2

		if err := f.SetCellValue(summarySheet, fmt.Sprintf("A%d", rowNum), stat.Name); err != nil {
			return "", err
		}
		if err := f.SetCellValue(summarySheet, fmt.Sprintf("B%d", rowNum), stat.InProgress); err != nil {
			return "", err
		}
		if err := f.SetCellValue(summarySheet, fmt.Sprintf("C%d", rowNum), stat.SentToTestToday); err != nil {
			return "", err
		}
		if err := f.SetCellValue(summarySheet, fmt.Sprintf("D%d", rowNum), stat.Backlog); err != nil {
			return "", err
		}
	}

	for i := range summaryHeaders {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(summarySheet, col, col, 25); err != nil {
			return "", err
		}
	}

	// активный лист – "Сводная"
	f.SetActiveSheet(summaryIdx)

	if err := os.MkdirAll(s.opts.SaveDir, 0o755); err != nil {
		return "", fmt.Errorf("make dir: %w", err)
	}

	timestamp := reportDate.Format("2006-01-02_15-04-05")
	fileName := fmt.Sprintf("5921_%s.xlsx", timestamp)
	filePath := filepath.Join(s.opts.SaveDir, fileName)

	if err := f.SaveAs(filePath); err != nil {
		return "", fmt.Errorf("save excel: %w", err)
	}

	return filePath, nil
}

func (s *OpenProjectService) writeTasksSheet(
	f *excelize.File,
	sheet string,
	headers []string,
	tasks []models.WorkPackage,
) error {
	// заголовки
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return err
		}
	}

	for idx, task := range tasks {
		row := idx + 2
		rowStr := strconv.Itoa(row)

		set := func(col string, v interface{}) error {
			return f.SetCellValue(sheet, col+rowStr, v)
		}

		if err := set("A", task.ID); err != nil {
			return err
		}
		if err := set("B", task.Subject); err != nil {
			return err
		}
		if err := set("C", task.Links.Type.Title); err != nil {
			return err
		}
		if err := set("D", task.Links.Status.Title); err != nil {
			return err
		}
		if err := set("E", task.Links.Assignee.Title); err != nil {
			return err
		}
		if err := set("F", task.Links.Responsible.Title); err != nil {
			return err
		}
		if err := set("G", task.Links.Project.Title); err != nil {
			return err
		}

		if task.StartDate != nil {
			if err := set("H", formatDateForExcel(*task.StartDate)); err != nil {
				return err
			}
		}
		if task.DueDate != nil {
			if err := set("I", formatDateForExcel(*task.DueDate)); err != nil {
				return err
			}
		}
	}

	for i := range headers {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(sheet, col, col, 20); err != nil {
			return err
		}
	}

	return nil
}

// ===================== Даты =====================

const dateLayout = "2006-01-02"

func parseDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	return time.Parse(dateLayout, dateStr)
}

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
