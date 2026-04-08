package seo

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

var minimalRSSLastBuildDate = time.Unix(0, 0).UTC()

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

// BuildRSS renders a deterministic RSS 2.0 feed for published notes.
func BuildRSS(cfg model.SiteConfig, notes []model.NoteSummary) ([]byte, error) {
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		return nil, fmt.Errorf("rss title is required")
	}

	channelLink := absolutePageURL(cfg.BaseURL, "")
	if channelLink == "" {
		return nil, fmt.Errorf("rss base URL is required")
	}

	sortedNotes := sortedRSSNotes(notes)
	items := make([]rssItem, 0, len(sortedNotes))
	for _, note := range sortedNotes {
		link := absolutePageURL(cfg.BaseURL, note.URL)
		if strings.TrimSpace(link) == "" {
			return nil, fmt.Errorf("rss item missing link")
		}

		itemTitle := strings.TrimSpace(note.Title)
		if itemTitle == "" {
			return nil, fmt.Errorf("rss item %q missing title", link)
		}

		publishedAt := normalizeRSSTime(note.Date)
		if publishedAt.IsZero() {
			return nil, fmt.Errorf("rss item %q missing pubDate", link)
		}

		items = append(items, rssItem{
			Title:       itemTitle,
			Link:        link,
			Description: strings.TrimSpace(note.Summary),
			PubDate:     publishedAt.Format(time.RFC1123Z),
			GUID:        link,
		})
	}

	description := strings.TrimSpace(cfg.Description)
	if description == "" {
		description = title
	}

	language := strings.TrimSpace(cfg.Language)
	if language == "" {
		language = "en"
	}

	body, err := xml.MarshalIndent(rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title:         title,
			Link:          channelLink,
			Description:   description,
			Language:      language,
			LastBuildDate: rssLastBuildDate(sortedNotes).Format(time.RFC1123Z),
			Items:         items,
		},
	}, "", "  ")
	if err != nil {
		return nil, err
	}

	output := append([]byte(xml.Header), body...)
	output = append(output, '\n')
	return output, nil
}

func sortedRSSNotes(notes []model.NoteSummary) []model.NoteSummary {
	sorted := append([]model.NoteSummary(nil), notes...)
	sort.SliceStable(sorted, func(i int, j int) bool {
		return lessRSSNoteSummary(sorted[i], sorted[j])
	})
	return sorted
}

func lessRSSNoteSummary(left model.NoteSummary, right model.NoteSummary) bool {
	leftDate := normalizeRSSTime(left.Date)
	rightDate := normalizeRSSTime(right.Date)

	switch {
	case leftDate.IsZero() && !rightDate.IsZero():
		return false
	case !leftDate.IsZero() && rightDate.IsZero():
		return true
	case !leftDate.Equal(rightDate):
		return leftDate.After(rightDate)
	}

	leftURL := rssSortKey(left.URL)
	rightURL := rssSortKey(right.URL)
	if leftURL != rightURL {
		return leftURL < rightURL
	}

	leftTitle := strings.TrimSpace(left.Title)
	rightTitle := strings.TrimSpace(right.Title)
	if leftTitle != rightTitle {
		return leftTitle < rightTitle
	}

	return strings.TrimSpace(left.Summary) < strings.TrimSpace(right.Summary)
}

func rssSortKey(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
}

func rssLastBuildDate(notes []model.NoteSummary) time.Time {
	latest := time.Time{}
	for _, note := range notes {
		publishedAt := normalizeRSSTime(note.Date)
		if publishedAt.IsZero() {
			continue
		}
		if latest.IsZero() || publishedAt.After(latest) {
			latest = publishedAt
		}
	}
	if latest.IsZero() {
		return minimalRSSLastBuildDate
	}
	return latest
}

func normalizeRSSTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Round(0).UTC().Truncate(time.Second)
}
