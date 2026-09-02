package model

// Breadcrumb is one server-rendered section navigation item.
type Breadcrumb struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
