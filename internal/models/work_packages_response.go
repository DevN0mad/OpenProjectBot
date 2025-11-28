package models

// WorkPackageResponse представляет ответ API с задачами
type WorkPackageResponse struct {
	Embedded struct {
		Elements []WorkPackage `json:"elements"`
	} `json:"_embedded"`

	Total int `json:"total"`
	Count int `json:"count"`

	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`

		Next *struct {
			Href string `json:"href"`
		} `json:"next,omitempty"`
	} `json:"_links"`
}
