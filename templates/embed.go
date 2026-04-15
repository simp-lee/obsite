// Package templates embeds the default site templates and built-in runtime assets.
package templates

import "embed"

// FS exposes the default site templates and stylesheet to the render package.
//
// Go's embed patterns are relative to the package directory, so this helper
// package lives alongside the template assets instead of under internal/render.
//
//go:embed *.html *.css *.js *.mjs
var FS embed.FS
