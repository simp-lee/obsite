package seo

import "strings"

// BuildRobots renders a permissive robots.txt with an absolute sitemap URL.
func BuildRobots(baseURL string) string {
	lines := []string{
		"User-agent: *",
		"Allow: /",
	}

	if sitemapURL := absolutePageURL(baseURL, "sitemap.xml"); sitemapURL != "" {
		lines = append(lines, "Sitemap: "+sitemapURL)
	}

	return strings.Join(lines, "\n") + "\n"
}
