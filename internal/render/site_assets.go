package render

import "embed"

//go:embed site/*
var embeddedSiteFS embed.FS

func embeddedSiteAssetPath(name string) string {
	return "site/" + name
}
