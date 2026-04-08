package render

import (
	"strings"

	"github.com/simp-lee/obsite/internal/model"
)

type tocStackEntry struct {
	level int
	entry *model.TOCEntry
}

func buildTOC(headings []model.Heading, pageTitle string, titleID string, omitLeadingTitle bool) []model.TOCEntry {
	usable := make([]model.Heading, 0, len(headings))
	normalizedTitle := normalizeHeadingText(pageTitle)
	trimmedTitleID := strings.TrimSpace(titleID)

	for index, heading := range headings {
		text := strings.TrimSpace(heading.Text)
		id := strings.TrimSpace(heading.ID)
		if heading.Level <= 0 || text == "" || id == "" {
			continue
		}

		if omitLeadingTitle && index == 0 && heading.Level == 1 && tocHeadingMatchesPromotedTitle(text, id, normalizedTitle, trimmedTitleID) {
			continue
		}

		usable = append(usable, model.Heading{
			Level: heading.Level,
			Text:  text,
			ID:    id,
		})
	}

	if len(usable) < 2 {
		return nil
	}

	root := make([]model.TOCEntry, 0, len(usable))
	stack := make([]tocStackEntry, 0, len(usable))
	for _, heading := range usable {
		entry := model.TOCEntry{Text: heading.Text, ID: heading.ID}

		for len(stack) > 0 && heading.Level <= stack[len(stack)-1].level {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			root = append(root, entry)
			stack = append(stack, tocStackEntry{level: heading.Level, entry: &root[len(root)-1]})
			continue
		}

		parent := stack[len(stack)-1].entry
		parent.Children = append(parent.Children, entry)
		stack = append(stack, tocStackEntry{level: heading.Level, entry: &parent.Children[len(parent.Children)-1]})
	}

	return root
}

func tocHeadingMatchesPromotedTitle(text string, id string, normalizedTitle string, titleID string) bool {
	if titleID != "" && strings.TrimSpace(id) == titleID {
		return true
	}

	if normalizedTitle == "" {
		return false
	}

	return strings.EqualFold(normalizeHeadingText(text), normalizedTitle)
}
