package seo

import (
	"bytes"
	"encoding/xml"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildRSSProducesRSS20FeedSortedByDate(t *testing.T) {
	t.Parallel()

	cfg := model.SiteConfig{
		Title:       "Field Notes",
		BaseURL:     "https://example.com/blog/",
		Description: "An editorial notebook.",
		Language:    "en",
	}

	latest := time.Date(2026, 4, 7, 11, 45, 0, 0, time.UTC)
	older := time.Date(2026, 4, 5, 8, 15, 0, 0, time.UTC)
	notes := []model.NoteSummary{
		{
			Title:   "Older Note",
			Summary: "Older summary.",
			URL:     "older-note/",
			Date:    older,
		},
		{
			Title:   "Beta Note",
			Summary: "Beta summary.",
			URL:     "beta-note/",
			Date:    latest,
		},
		{
			Title:   "Alpha Note",
			Summary: "Alpha summary.",
			URL:     "alpha-note/",
			Date:    latest,
		},
	}

	got, err := BuildRSS(cfg, notes)
	if err != nil {
		t.Fatalf("BuildRSS() error = %v", err)
	}
	if !bytes.HasPrefix(got, []byte(xml.Header)) {
		t.Fatalf("BuildRSS() missing XML header\n%s", got)
	}

	var parsed struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel struct {
			Title         string `xml:"title"`
			Link          string `xml:"link"`
			Description   string `xml:"description"`
			Language      string `xml:"language"`
			LastBuildDate string `xml:"lastBuildDate"`
			Items         []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
				GUID        string `xml:"guid"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}

	if parsed.Version != "2.0" {
		t.Fatalf("rss version = %q, want %q", parsed.Version, "2.0")
	}
	if parsed.Channel.Title != cfg.Title {
		t.Fatalf("channel.title = %q, want %q", parsed.Channel.Title, cfg.Title)
	}
	if parsed.Channel.Link != "https://example.com/blog/" {
		t.Fatalf("channel.link = %q, want %q", parsed.Channel.Link, "https://example.com/blog/")
	}
	if parsed.Channel.Description != cfg.Description {
		t.Fatalf("channel.description = %q, want %q", parsed.Channel.Description, cfg.Description)
	}
	if parsed.Channel.Language != cfg.Language {
		t.Fatalf("channel.language = %q, want %q", parsed.Channel.Language, cfg.Language)
	}
	if parsed.Channel.LastBuildDate != latest.Format(time.RFC1123Z) {
		t.Fatalf("channel.lastBuildDate = %q, want %q", parsed.Channel.LastBuildDate, latest.Format(time.RFC1123Z))
	}

	if len(parsed.Channel.Items) != 3 {
		t.Fatalf("len(channel.items) = %d, want %d", len(parsed.Channel.Items), 3)
	}

	wantLinks := []string{
		"https://example.com/blog/alpha-note/",
		"https://example.com/blog/beta-note/",
		"https://example.com/blog/older-note/",
	}
	wantTitles := []string{"Alpha Note", "Beta Note", "Older Note"}
	wantDescriptions := []string{"Alpha summary.", "Beta summary.", "Older summary."}
	wantDates := []string{latest.Format(time.RFC1123Z), latest.Format(time.RFC1123Z), older.Format(time.RFC1123Z)}
	for index := range wantLinks {
		item := parsed.Channel.Items[index]
		if item.Title != wantTitles[index] {
			t.Fatalf("items[%d].title = %q, want %q", index, item.Title, wantTitles[index])
		}
		if item.Link != wantLinks[index] {
			t.Fatalf("items[%d].link = %q, want %q", index, item.Link, wantLinks[index])
		}
		if item.Description != wantDescriptions[index] {
			t.Fatalf("items[%d].description = %q, want %q", index, item.Description, wantDescriptions[index])
		}
		if item.PubDate != wantDates[index] {
			t.Fatalf("items[%d].pubDate = %q, want %q", index, item.PubDate, wantDates[index])
		}
		if item.GUID != wantLinks[index] {
			t.Fatalf("items[%d].guid = %q, want %q", index, item.GUID, wantLinks[index])
		}
	}
}

func TestBuildRSSUsesLatestLastModifiedForChannelLastBuildDate(t *testing.T) {
	t.Parallel()

	cfg := model.SiteConfig{
		Title:   "Field Notes",
		BaseURL: "https://example.com/blog/",
	}

	olderPublished := time.Date(2026, 4, 5, 8, 15, 0, 0, time.UTC)
	olderUpdated := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	newerPublished := time.Date(2026, 4, 7, 11, 45, 0, 0, time.UTC)
	notes := []model.NoteSummary{
		{
			Title:        "Older Note",
			Summary:      "Updated after publication.",
			URL:          "older-note/",
			Date:         olderPublished,
			LastModified: olderUpdated,
		},
		{
			Title:        "Newer Note",
			Summary:      "Newer publication.",
			URL:          "newer-note/",
			Date:         newerPublished,
			LastModified: newerPublished,
		},
	}

	got, err := BuildRSS(cfg, notes)
	if err != nil {
		t.Fatalf("BuildRSS() error = %v", err)
	}

	var parsed struct {
		Channel struct {
			LastBuildDate string `xml:"lastBuildDate"`
			Items         []struct {
				Title   string `xml:"title"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}

	if parsed.Channel.LastBuildDate != olderUpdated.Format(time.RFC1123Z) {
		t.Fatalf("channel.lastBuildDate = %q, want %q", parsed.Channel.LastBuildDate, olderUpdated.Format(time.RFC1123Z))
	}
	if len(parsed.Channel.Items) != 2 {
		t.Fatalf("len(channel.items) = %d, want %d", len(parsed.Channel.Items), 2)
	}
	if parsed.Channel.Items[0].Title != "Newer Note" {
		t.Fatalf("items[0].title = %q, want %q", parsed.Channel.Items[0].Title, "Newer Note")
	}
	if parsed.Channel.Items[0].PubDate != newerPublished.Format(time.RFC1123Z) {
		t.Fatalf("items[0].pubDate = %q, want %q", parsed.Channel.Items[0].PubDate, newerPublished.Format(time.RFC1123Z))
	}
	if parsed.Channel.Items[1].Title != "Older Note" {
		t.Fatalf("items[1].title = %q, want %q", parsed.Channel.Items[1].Title, "Older Note")
	}
	if parsed.Channel.Items[1].PubDate != olderPublished.Format(time.RFC1123Z) {
		t.Fatalf("items[1].pubDate = %q, want %q", parsed.Channel.Items[1].PubDate, olderPublished.Format(time.RFC1123Z))
	}
}
