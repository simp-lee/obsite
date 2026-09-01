# Obsite

Obsite is a self-contained CLI that turns one Obsidian Markdown vault into a static website.

## Quick start

Run these commands from the parent of an existing vault directory:

```bash
cd vault
obsite init
obsite build
obsite serve --watch
```

Open <http://localhost:8080>. `init` creates `obsite.yaml` exclusively and will not overwrite an existing file. The generated configuration builds immediately; replace its example `baseURL` with the real public URL before publishing.

The current directory is the default vault. `build` reads `<vault>/obsite.yaml` and publishes transactionally to `<vault>/public`. `serve --watch` performs that same build first, then watches valid vault inputs and reloads the browser only after a successful rebuild.

## Install

Download the archive for Linux, macOS, or Windows (`amd64` or `arm64`) from [GitHub Releases](https://github.com/simp-lee/obsite/releases). Each archive contains one `obsite` executable, the project license, and third-party notices. Verify the archive with the published SHA-256 `checksums.txt` before use.

The versioned Go entrypoint is also supported and requires Go 1.25 or newer:

```bash
go install github.com/simp-lee/obsite/cmd/obsite@vX.Y.Z
```

Official release archives contain one `CGO_ENABLED=0` binary; source installs honor the caller's Go and CGO environment. Running `init`, `build`, or `serve` does not require Node.js, npm, an external search indexer, a browser tool, a CDN, another executable, or a runtime download. Obsite does not expose a public Go library API.

## Commands

```text
obsite init [--vault PATH]
obsite build [--vault PATH] [--output PATH] [--force]
obsite serve [--vault PATH] [--output PATH] [--port NUM] [--watch]
obsite version
obsite --version
```

- `init` defaults to the current directory. The vault must already exist.
- `build` defaults to the current directory and `<vault>/public`. `--output` may select another directory; `--force` bypasses page-cache reuse.
- `serve` defaults to `<vault>/public` on port `8080`. Without `--watch`, a missing output is an error. With `--watch`, Obsite builds before serving.
- The optional `--vault` changes the vault root. There is no external config or theme flag.
- Running `obsite` without arguments prints concise help. Unknown commands, arguments, and removed flags fail explicitly.

Shell completions are available through `obsite completion <bash|zsh|fish|powershell>`.

## Vault contract

The vault is the only file data source. Obsite recognizes these fixed inputs:

- `obsite.yaml` — the only site configuration.
- Markdown notes and publishable vault resources.
- `.obsidian/app.json` — only for Obsidian's `attachmentFolderPath` setting.
- `custom.css` — an optional final user stylesheet.
- `.obsite/theme/theme.css` — an optional CSS-variable theme.
- `.obsite/theme/slots.html` — optional append-only HTML slots.
- `.obsite/theme/assets/**` — optional theme assets.

Ordinary scanning excludes `.obsite`, `.obsidian`, `node_modules`, unrelated hidden paths, and the resolved output directory. Config, CSS, theme files, Markdown, and resources must be regular contained vault files; symlink escapes are rejected. `serve --watch` watches the fixed inputs above and excludes the generated output.

## Configuration

Only `title` and an absolute HTTP(S) `baseURL` are required. YAML is strict: unknown and removed fields are errors.

```yaml
baseURL: https://example.com/notes/
title: My Obsite Site
author: ""
description: ""
language: en
defaultPublish: true
defaultImg: ""
pagination:
  pageSize: 20
sidebar:
  enabled: false
popover:
  enabled: false
related:
  enabled: false
  count: 5
rss:
  enabled: true
timeline:
  enabled: false
  asHomepage: false
  path: notes
```

`baseURL` and `title` above are required examples; the remaining values shown are the product defaults. `related.count` must be from `1` through `20`. Related notes use build-time dynamic TF-IDF cosine similarity plus source-link and normalized-tag signals; disabling related notes avoids tokenizer and recommendation-index work.

### `defaultImg`

`defaultImg` has exactly two non-empty meanings:

1. An absolute `http` or `https` URL with a host is used as the hosted Open Graph/Twitter image without local lookup.
2. Any other value must be a clean `/`-separated resource path relative to the vault root, such as `media/social-card.png`.

A local image is resolved from the vault root, published even when no note references it, and routed through the same collision-safe asset plan as other resources. Metadata uses its final public URL. Missing, ambiguous, directory, symlink, absolute filesystem, query/fragment, backslash, or escaping paths fail the build. An empty value emits no image metadata.

## Markdown and generated site

Obsite keeps page content server-rendered and supports frontmatter publishing/metadata, wikilinks, note and image embeds, callouts, comment stripping, hashtags, headings/TOC, syntax highlighting, permitted raw HTML, attachments, backlinks, tags, folders, pagination, timeline, RSS, sitemap, robots, canonical metadata, Open Graph, Twitter cards, JSON-LD, and a static 404 page.

The stable note, index, tag, folder, timeline, and 404 shells are built into Obsite. Themes cannot replace page structure, SEO, headers, content, navigation mounts, or footers. Stable integrations can target `data-obsite-root`, `data-obsite-kind`, `data-obsite-main`, and `data-page-content`; decorative classes are not compatibility promises.

### Offline math and diagrams

Obsite embeds unmodified official browser distributions of KaTeX `0.18.4` and Mermaid `11.17.2`, including KaTeX fonts. Math source is protected by the pinned upstream Goldmark passthrough extension. Generated sites render math and diagrams without CDN, npm, or other network requests. Pages without a feature do not start its vendor runtime.

### Shared runtime, Sidebar, and Popover

Every page references one content-addressed `assets/obsite/runtime.<hash>.js`. It applies the base-path-isolated light/dark preference before paint, then initializes enabled enhancements. When Sidebar is enabled, the tree is serialized once at `assets/obsite/sidebar.json`; it is not copied into each page. Popovers use delegated behavior for both server-rendered note links and Sidebar links created later.

JavaScript or shared-data failure does not remove the server-rendered title, body, breadcrumbs, built-in links, SEO, or static resources. Sidebar and Popover are progressive enhancements.

## CSS themes and assets

Stylesheets always load in this order:

1. Obsite structural CSS and default light/dark/system variables.
2. Official runtime CSS.
3. Optional `.obsite/theme/theme.css`, published as `assets/theme/theme.css`.
4. Optional vault-root `custom.css`, published as `assets/custom.css`.

A distributable theme changes the public variables rather than replacing HTML:

```css
:root {
  --obsite-background: #fff;
  --obsite-surface: #f7f7f7;
  --obsite-text: #171717;
  --obsite-muted: #686868;
  --obsite-accent: #315efb;
  --obsite-border: #dedede;
  --obsite-font-body: system-ui, sans-serif;
  --obsite-font-display: system-ui, sans-serif;
  --obsite-font-mono: ui-monospace, monospace;
  --obsite-content-width: 48rem;
  --obsite-sidebar-width: 18rem;
  --obsite-radius: 0.5rem;
  --obsite-shadow: 0 0.5rem 1.5rem rgb(0 0 0 / 12%);
}
```

Files below `.obsite/theme/assets/` are copied without that source `assets/` prefix to `assets/theme/`. Relative URLs in `theme.css` therefore remain relative to the published theme stylesheet. `custom.css` may use arbitrary CSS as the final site-specific override.

## Append-only theme slots

`.obsite/theme/slots.html` may contain only Go `html/template` definitions for these four names:

- `obsite-head-end`
- `obsite-header-end`
- `obsite-main-end`
- `obsite-footer-end`

Each is appended exactly once at its named structural boundary. Example:

```gotemplate
{{ define "obsite-footer-end" }}
  <img src="{{ themeAssetURL .SiteRootRel "brand/mark.svg" }}" alt="">
{{ end }}
```

Slots can read only `.Kind`, `.Title`, `.Canonical`, `.RelPath`, `.SiteRootRel`, `.Site.Title`, `.Site.BaseURL`, `.Site.Author`, `.Site.Description`, and `.Site.Language`. The only added helper is `themeAssetURL .SiteRootRel "path-relative-to-theme-assets"`. Unknown definitions, raw content outside definitions, template invocation, unsupported files in `.obsite/theme/`, and invalid asset paths fail the build.

## Note frontmatter

```yaml
---
title: My Note Title
description: A short SEO description.
publish: true
date: 2026-01-15
updated: 2026-02-10
tags: [example, notes]
aliases: [alternate-name]
slug: custom-url-slug
---
```

`publish` overrides `defaultPublish`; title falls back to the filename. Dates, aliases, tags, descriptions, and custom slugs feed the corresponding page, link, list, related-note, and SEO behavior.

## Development

Pinned development versions used by CI are Go `1.26.2`, Node.js `24.14.1`, Playwright `1.62.1`, Chromium from that exact Playwright package, GoReleaser `v2.18.0`, actionlint `v1.7.7`, golangci-lint `v2.11.4`, gofumpt `v0.9.2`, and goimports `v0.44.0`.

```bash
make tools       # install the pinned Go format/lint tools
make check       # format check, lint, Go tests, and product/workflow audits
npm ci
npx --no-install playwright install chromium
npm run test:e2e
```

Release verification is available through the pinned wrappers:

```bash
sh scripts/verify-version-builds.sh
scripts/goreleaser.sh check
scripts/goreleaser.sh release --snapshot --clean
sh scripts/verify-release.sh dist
```
