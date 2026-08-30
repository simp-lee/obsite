package model

// RelatedSemanticDocument is the build-owned source semantic input for one
// public note. It is deliberately separate from Note and page/cache models.
type RelatedSemanticDocument struct {
	RelPath  string
	Title    string
	Aliases  []string
	Headings []string
	Body     string
}
