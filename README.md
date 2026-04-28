# Obsite

Obsite is a static site generator that turns an [Obsidian](https://obsidian.md/) vault into a fast, SEO-friendly website. It is distributed as a single Go binary with no runtime dependencies. Optional full-text search uses the external `pagefind_extended` binary during builds.

## Features

- **Obsidian-native Markdown** — wikilinks, callouts, embeds, comments, LaTeX math, hashtags
- **Incremental builds** — content-hash cache rebuilds only changed pages
- **Full-text search** — optional [Pagefind](https://pagefind.app/) integration
- **SEO** — canonical URLs, Open Graph, Twitter Cards, JSON-LD, sitemap, robots.txt, RSS
- **Related articles** — BM25 text similarity with tag and wikilink boosting
- **Live preview** — local server with file watching and live reload
- **Sidebar navigation** — collapsible file-tree sidebar
- **Link popovers** — async internal-link previews
- **Timeline page** — chronological recent-notes view, optionally as homepage
- **KaTeX & Mermaid** — client-side math and diagram rendering
- **Syntax highlighting** — Chroma-based code highlighting
- **HTML & CSS minification** — smaller output files
- **Named build-time themes** — select a configured theme with `--theme` or `defaultTheme`
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

# Optional: select a configured theme for this build
obsite build --vault ~/my-vault --output ~/my-site --theme feature

# 4. Preview locally
obsite serve --output ~/my-site

# Optional: rebuild and preview a configured theme while watching the vault
obsite serve --output ~/my-site --watch --vault ~/my-vault --theme feature
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
obsite build --vault <PATH> --output <PATH> [--config <PATH>] [--theme <NAME>] [--force]
```

| Flag | Description |
|---|---|
| `--vault` | Path to the Obsidian vault (required) |
| `--output` | Path to write the generated site (required) |
| `--config` | Path to `obsite.yaml` (defaults to `<vault>/obsite.yaml`) |
| `--theme` | Theme name declared under `themes`; overrides `defaultTheme` for this build |
| `--force` | Ignore incremental cache and rebuild all pages |

### `obsite serve`

Serve the generated site for local preview.

```bash
obsite serve --output <PATH> [--port <NUM>]
obsite serve --output <PATH> --watch --vault <PATH> [--config <PATH>] [--theme <NAME>] [--port <NUM>]
```

| Flag | Description |
|---|---|
| `--output` | Path to the generated site (required) |
| `--vault` | Path to the vault (required when `--watch` is used) |
| `--config` | Path to `obsite.yaml` (defaults to `<vault>/obsite.yaml` when `--watch` is used) |
| `--theme` | Theme name declared under `themes`; only valid together with `--watch` |
| `--port` | Port number (default: `8080`) |
| `--watch` | Rebuild on file changes and live-reload browsers |

Static preview mode serves the already-generated output directory and rejects `--theme`. Use `--watch` when you need the server to rebuild with a specific configured theme.

### `obsite completion`

Generate shell completion scripts.

```bash
obsite completion <bash|zsh|fish|powershell>
```

## Configuration

All settings live in `obsite.yaml`, typically placed at the vault root. Run `obsite init` to generate a commented template.

Only `baseURL` and `title` are required. Filesystem paths such as `search.pagefindPath` and `themes.<name>.root` are resolved relative to `obsite.yaml` unless they are absolute paths. There are no `templateDir` or `customCSS` config keys: theme selection lives under `themes` and `defaultTheme`, and Obsite only auto-detects `custom.css` from the vault root.

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

# Search (requires pagefind_extended)
search:
  enabled: false
  pagefindPath: tools/pagefind_extended  # Relative to obsite.yaml unless absolute
  pagefindVersion: 1.5.2

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
  count: 5                # Recommendations per page

# RSS feed
rss:
  enabled: true

# Timeline / recent-notes page
timeline:
  enabled: false
  asHomepage: false       # Replace the default homepage
  path: notes             # Output path for the timeline

# Named build-time themes
themes:
  feature:
    root: themes/feature  # Relative to obsite.yaml unless absolute
defaultTheme: feature     # Optional fallback when --theme is omitted
```

Place an optional global override stylesheet at `<vault>/custom.css` to load it after the generated site stylesheet. That vault-root file is the only auto-detected `custom.css` location.

External theme roots are complete build-time themes. Each selected theme root must provide every required HTML template. Any HTML file under the theme root is treated as template input, not as a published static asset; theme-owned static assets are limited to non-HTML regular files. Use `themes`, `defaultTheme`, or `--theme` to select the active theme for a build.

If a theme supports search, keep the Pagefind UI nodes in `base.html` marked with `data-obsite-search-ui`. The build pipeline relies on that marker contract for the search stylesheet and container when `search.enabled: true`.

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

## Search Setup

Obsite supports full-text search via Pagefind. Search indexing uses the `pagefind_extended` binary, and the helper installer in this repository requires `npm`.

```bash
# From a checkout of this repository, install pagefind_extended into your vault
make install-pagefind PROJECT_DIR=~/my-vault
# or directly:
sh scripts/install-pagefind.sh ~/my-vault
```

This places the binary at `~/my-vault/tools/pagefind_extended`, which matches the default `search.pagefindPath` generated by `obsite init`.

Then enable search in `obsite.yaml`:

```yaml
search:
  enabled: true
  pagefindPath: tools/pagefind_extended
  pagefindVersion: 1.5.2
```

If you install Pagefind elsewhere, update `search.pagefindPath` to an absolute path or a path relative to `obsite.yaml`. The `PAGEFIND_VERSION` environment variable overrides the default version during installation.

## Build Pipeline

Obsite processes a vault through these phases:

1. **Scan** — Walk the vault, discover Markdown files and attachments
2. **Frontmatter** — Parse YAML frontmatter, partition public/unpublished notes
3. **Index** — Build the vault index with slugs, aliases, and tags
4. **Render (Pass 1)** — Convert Markdown to HTML, resolve wikilinks, discover assets
5. **Link Graph & Related** — Build backlinks graph, compute BM25-based recommendations
6. **Render (Pass 2)** — Apply templates with HTML minification, write popover payloads
7. **Assets** — Emit CSS and runtime assets, copy vault resources with hash-based deduplication
8. **SEO** — Generate `robots.txt`, `sitemap.xml`, `index.xml`, JSON-LD
9. **Search** — Run Pagefind indexing (when enabled)

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

