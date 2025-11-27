package models

// WorkPackageResponse представляет ответ API с задачами
type WorkPackageResponse struct {
	Embedded struct {
		Elements []WorkPackage `json:"elements"`
	} `json:"_embedded"`
	Total int `json:"total"`
}
