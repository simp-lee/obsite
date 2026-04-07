package seo

import (
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

const sitemapXMLNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// BuildSitemap renders deterministic sitemap.xml contents for the supplied public pages.
// Every emitted page must carry a canonical URL and explicit last-modified timestamp.
func BuildSitemap(pages []model.PageData) ([]byte, error) {
	sortedPages := sortedSitemapPages(pages)
	urls := make([]sitemapURL, 0, len(sortedPages))
	for _, page := range sortedPages {
		loc := strings.TrimSpace(page.Canonical)
		if loc == "" {
			return nil, fmt.Errorf("sitemap page missing canonical URL")
		}

		lastModified := normalizeSitemapTime(page.LastModified)
		if lastModified.IsZero() {
			return nil, fmt.Errorf("sitemap entry %q missing lastmod", loc)
		}

		urls = append(urls, sitemapURL{
			Loc:     loc,
			LastMod: lastModified.Format(time.RFC3339),
		})
	}

	body, err := xml.MarshalIndent(sitemapURLSet{
		XMLNS: sitemapXMLNS,
		URLs:  urls,
	}, "", "  ")
	if err != nil {
		return nil, err
	}

	output := append([]byte(xml.Header), body...)
	output = append(output, '\n')
	return output, nil
}

func normalizeSitemapTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}

	return value.Round(0).UTC().Truncate(time.Second)
}

func sortedSitemapPages(pages []model.PageData) []model.PageData {
	sorted := append([]model.PageData(nil), pages...)
	sort.SliceStable(sorted, func(i int, j int) bool {
		return lessSitemapPage(sorted[i], sorted[j])
	})
	return sorted
}

func lessSitemapPage(left model.PageData, right model.PageData) bool {
	leftRecency := sitemapPageRecency(left)
	rightRecency := sitemapPageRecency(right)

	switch {
	case leftRecency.IsZero() && !rightRecency.IsZero():
		return false
	case !leftRecency.IsZero() && rightRecency.IsZero():
		return true
	case !leftRecency.Equal(rightRecency):
		return leftRecency.After(rightRecency)
	}

	leftKey, leftHasKey := sitemapPageSortKey(left)
	rightKey, rightHasKey := sitemapPageSortKey(right)
	switch {
	case leftHasKey && !rightHasKey:
		return true
	case !leftHasKey && rightHasKey:
		return false
	case leftHasKey && rightHasKey && leftKey != rightKey:
		return leftKey < rightKey
	}

	return strings.TrimSpace(left.Canonical) < strings.TrimSpace(right.Canonical)
}

func sitemapPageRecency(page model.PageData) time.Time {
	if !page.Date.IsZero() {
		return normalizeSitemapTime(page.Date)
	}
	return normalizeSitemapTime(page.LastModified)
}

func sitemapPageSortKey(page model.PageData) (string, bool) {
	if slug := normalizeSitemapSortKey(page.Slug); strings.TrimSpace(page.Slug) != "" {
		return slug, true
	}
	if strings.TrimSpace(page.RelPath) == "" {
		return "", false
	}
	return normalizeSitemapSortKey(page.RelPath), true
}

func normalizeSitemapSortKey(value string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if trimmed == "" {
		return ""
	}

	clean := path.Clean(trimmed)
	if clean == "." {
		return ""
	}

	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimSuffix(clean, "/index.html")
	clean = strings.Trim(clean, "/")
	if clean == "index.html" {
		return ""
	}
	return clean
}
