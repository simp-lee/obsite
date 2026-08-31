# Obsite

Obsite is a single-binary CLI that builds a static website from an Obsidian vault.

## Features

- **Obsidian-native Markdown** — wikilinks, callouts, embeds, comments, LaTeX math, hashtags
- **Incremental builds** — content-hash cache rebuilds only changed pages
- **SEO** — canonical URLs, Open Graph, Twitter Cards, JSON-LD, sitemap, robots.txt, RSS
- **Related articles** — build-time dynamic TF-IDF cosine similarity with direct link and tag signals
- **Live preview** — local server with file watching and live reload
- **Sidebar navigation** — collapsible file-tree sidebar
- **Link popovers** — async internal-link previews
- **Timeline page** — chronological recent-notes view, optionally as homepage
- **KaTeX & Mermaid** — client-side math and diagram rendering
- **Syntax highlighting** — Chroma-based code highlighting
- **HTML & CSS minification** — smaller output files
- **Global custom.css override** — optional vault-root stylesheet loaded after the active theme

## Installation

```bash
go install github.com/simp-lee/obsite/cmd/obsite@latest
```

Requires **Go 1.25+**.

## Quick Start

```bash
# 1. Generate a config file inside your vault
obsite init --vault ~/my-vault

# 2. Edit the generated obsite.yaml
#    baseURL and title are required; author and description are recommended

# 3. Build the site
obsite build --vault ~/my-vault --output ~/my-site

# 4. Preview locally
obsite serve --output ~/my-site

# Optional: rebuild while watching the vault
obsite serve --output ~/my-site --watch --vault ~/my-vault
```

Open <http://localhost:8080> to view the site.

Only `baseURL` and `title` are required in `obsite.yaml`.

## Commands

### `obsite init`

Create an `obsite.yaml` template in a vault directory. The command fails if `obsite.yaml` already exists.

```bash
obsite init --vault <PATH>
```

### `obsite build`

Build a static site from an Obsidian vault.

```bash
```

| Flag | Description |
|---|---|
| `--vault` | Path to the Obsidian vault (required) |
| `--output` | Path to write the generated site (required) |
| `--config` | Path to `obsite.yaml` (defaults to `<vault>/obsite.yaml`) |
| `--force` | Ignore incremental cache and rebuild all pages |

### `obsite serve`

Serve the generated site for local preview.

```bash
obsite serve --output <PATH> [--port <NUM>]
```

| Flag | Description |
|---|---|
| `--output` | Path to the generated site (required) |
| `--vault` | Path to the vault (required when `--watch` is used) |
| `--config` | Path to `obsite.yaml` (defaults to `<vault>/obsite.yaml` when `--watch` is used) |
| `--port` | Port number (default: `8080`) |
| `--watch` | Rebuild on file changes and live-reload browsers |


### `obsite completion`

Generate shell completion scripts.

```bash
obsite completion <bash|zsh|fish|powershell>
```

## Configuration

All site settings live in the vault-root `obsite.yaml`; Obsite does not read external configuration files. Run `obsite init` to generate the current field set.


```yaml
# Required
baseURL: https://example.com/
title: My Obsite Site

# Recommended
author: Your Name
description: Notes published with obsite.

# Optional
language: en              # HTML lang attribute (default: en)
defaultPublish: true      # Publish notes without frontmatter publish field
defaultImg:               # Default Open Graph image path or absolute URL

# Pagination
pagination:
  pageSize: 20            # Items per page on list pages

# Sidebar file-tree navigation
sidebar:
  enabled: false

# Internal-link hover previews
popover:
  enabled: false

# Related articles
related:
  enabled: false
  count: 5                # Recommendations per page (1..20)

# RSS feed
rss:
  enabled: true

# Timeline / recent-notes page
timeline:
  enabled: false
  asHomepage: false       # Replace the default homepage
  path: notes             # Output path for the timeline

```

When enabled, related articles are ranked during the build with site-dynamic TF-IDF cosine similarity plus direct source-link and normalized-tag signals. `related.count` must be between `1` and `20`; omitting it uses `5`. The generated site remains fully static.

Place an optional global override stylesheet at `<vault>/custom.css` to load it after the generated site stylesheet. That vault-root file is the only auto-detected `custom.css` location.


## Note Frontmatter

Individual notes can use YAML frontmatter to control publishing and metadata:

```yaml
---
title: My Note Title
description: A short description for SEO.
publish: true
date: 2025-01-15
updated: 2025-02-10
tags:
  - example
  - notes
aliases:
  - alternate-name
slug: custom-url-slug
---
```

| Field | Description |
|---|---|
| `title` | Page title (falls back to filename) |
| `description` | SEO meta description |
| `publish` | `true` / `false` — overrides `defaultPublish` |
| `date` | Publication date |
| `updated` | Override last-modified timestamp |
| `tags` | List of tags |
| `aliases` | Alternative note names for wikilink resolution |
| `slug` | Custom URL slug (overrides auto-generated slug) |

## Build Pipeline

Obsite processes a vault through these phases:

1. **Scan** — Walk the vault, discover Markdown files and attachments
2. **Frontmatter** — Parse YAML frontmatter, partition public/unpublished notes
3. **Source index** — Build slugs, aliases, tags, direct links, and optional related-article semantic fields
4. **Related ranking** — Before HTML rendering, compute dynamic TF-IDF cosine features and combine qualified content with source-only link and tag signals
5. **Markdown render** — Convert Markdown to HTML, expand embeds, resolve render-time links, and discover assets
6. **Pages & graph** — Build the render-expanded backlinks graph and apply templates with related cards
7. **Assets** — Emit CSS and runtime assets, copy vault resources with hash-based deduplication
8. **SEO** — Generate `robots.txt`, `sitemap.xml`, `index.xml`, JSON-LD

Incremental builds skip unchanged pages using content-hash caching. Use `--force` to bypass the cache.

## Development

```bash
# Run all checks (format, lint, test)
make check

# Individual targets
make fmt          # Format code
make fmt-check    # Check formatting
make lint         # Run golangci-lint
make test         # Run tests
```

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [golangci-lint](https://golangci-lint.run/)
- [gofumpt](https://github.com/mvdan/gofumpt) (`go install mvdan.cc/gofumpt@latest`)
- [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) (`go install golang.org/x/tools/cmd/goimports@latest`)

