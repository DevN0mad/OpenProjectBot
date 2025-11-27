package models

// EmployeeStats представляет статистику по сотруднику
type EmployeeStats struct {
	Name            string
	InProgress      int
	SentToTestToday int
	Backlog         int
}
