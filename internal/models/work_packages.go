package models

// WorkPackage представляет задачу в OpenProject
type WorkPackage struct {
	ID      int    `json:"id"`
	Subject string `json:"subject"`
	Links   struct {
		Type struct {
			Title string `json:"title"`
			Href  string `json:"href"`
		} `json:"type"`
		Status struct {
			Title string `json:"title"`
			Href  string `json:"href"`
		} `json:"status"`
		Priority struct {
			Title string `json:"title"`
			Href  string `json:"href"`
		} `json:"priority"`
		Assignee struct {
			Title string `json:"title"`
			Href  string `json:"href"`
		} `json:"assignee"`
		Responsible struct {
			Title string `json:"title"`
		} `json:"responsible"`
		Project struct {
			Title string `json:"title"`
			Href  string `json:"href"`
		} `json:"project"`
	} `json:"_links"`

	StartDate      *string `json:"startDate"`
	DueDate        *string `json:"dueDate"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	TestPassedDate *string `json:"customField14"`
}
