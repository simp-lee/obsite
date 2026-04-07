// Package templates embeds the default site templates and stylesheet.
package templates

import "embed"

// FS exposes the default site templates and stylesheet to the render package.
//
// Go's embed patterns are relative to the package directory, so this helper
// package lives alongside the template assets instead of under internal/render.
//
//go:embed *.html style.css
var FS embed.FS
