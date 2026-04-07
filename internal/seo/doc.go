// Package seo derives deterministic page metadata, structured data, and site-level
// SEO artifacts such as sitemap.xml and robots.txt for rendered pages. Sitemap
// generation consumes page canonical URLs plus explicit page last-modified
// timestamps. Note-page Article JSON-LD falls back from frontmatter date to the
// note's normalized source timestamp before failing closed on any still-missing
// required schema fields, while valid BreadcrumbList JSON-LD remains emitted and
// site title provides the deterministic author fallback when an explicit site
// author is absent.
package seo
