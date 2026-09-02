package model

// SidebarNode is the compact deterministic tree consumed by the offline
// sidebar runtime and by the server-rendered no-JavaScript shell.
type SidebarNode struct {
	Name     string        `json:"name"`
	URL      string        `json:"url"`
	IsDir    bool          `json:"isDir,omitempty"`
	Children []SidebarNode `json:"children,omitempty"`
}
