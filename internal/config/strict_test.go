package config

import (
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestLoadStrictForBuildRequiresRevisedNavigationAndRejectsLegacyPublishDefault(t *testing.T) {
	for name, content := range map[string]string{
		"missing navigation": "title: Site\nbaseURL: https://example.test/\n",
		"legacy default":     "title: Site\nbaseURL: https://example.test/\nnavigation: []\ndefaultPublish: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			vault := writeConfigVault(t, content)
			if _, err := LoadStrictForBuild(vault); err == nil {
				t.Fatal("LoadStrictForBuild() error = nil, want schema error")
			}
		})
	}
}

func TestLoadStrictForBuildNormalizesNavigationSourceAndVersions(t *testing.T) {
	vault := writeConfigVault(t, `title: Site
baseURL: https://example.test/base
navigation:
  - name: Home
    section: .
  - name: Repository
    url: https://git.example.test:8443/source/:path?ref=main
source:
  editURL: https://git.example.test/edit/:path
versions:
  root: docs
  default: v1
  entries:
    - id: v1
      label: Version 1
      source: v1
`)
	cfg, err := LoadStrictForBuild(vault)
	if err != nil {
		t.Fatalf("LoadStrictForBuild() error = %v", err)
	}
	if len(cfg.Navigation) != 2 || cfg.Navigation[0].Section != "." || cfg.Navigation[1].URL == "" {
		t.Fatalf("navigation = %#v", cfg.Navigation)
	}
	if cfg.Source.EditURL != "https://git.example.test/edit/:path" || cfg.Versions == nil || cfg.Versions.Entries[0].ID != "v1" {
		t.Fatalf("normalized config = %#v", cfg)
	}
}

func TestNormalizeSiteConfigRejectsSourceTemplatePlaceholdersAndVersionOverlap(t *testing.T) {
	_, err := NormalizeSiteConfig(structuredConfig(model.SourceConfig{ViewURL: "https://example.test:8443/view/:path/:branch"}, nil))
	if err == nil || !strings.Contains(err.Error(), "unknown template placeholder") {
		t.Fatalf("source error = %v, want unknown placeholder", err)
	}
	versions := &model.VersionsConfig{Root: "docs", Default: "v1", Entries: []model.VersionEntry{
		{ID: "v1", Label: "one", Source: "v1"}, {ID: "v2", Label: "two", Source: "v1/chapter"},
	}}
	_, err = NormalizeSiteConfig(structuredConfig(model.SourceConfig{}, versions))
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("version error = %v, want overlap rejection", err)
	}
}

func structuredConfig(source model.SourceConfig, versions *model.VersionsConfig) model.SiteConfig {
	cfg := Defaults()
	cfg.Title, cfg.BaseURL = "Site", "https://example.test/"
	cfg.Source, cfg.Versions = source, versions
	return cfg
}
