package build

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestOutputOwnerRegistryRejectsCrossOwnerAndAncestorCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims func(*outputDestinationPlan) error
	}{
		{
			name: "same file",
			claims: func(plan *outputDestinationPlan) error {
				if err := plan.claimFile("assets/custom.css", outputOwnerCustomCSS, "custom.css"); err != nil {
					return err
				}
				return plan.claimFile("assets/CUSTOM.css", outputOwnerVaultAsset, "images/custom.css")
			},
		},
		{
			name: "file is ancestor",
			claims: func(plan *outputDestinationPlan) error {
				if err := plan.claimFile("style.css/index.html", outputOwnerPage, "style.css folder"); err != nil {
					return err
				}
				return plan.claimFile("style.css", outputOwnerStyle, "site stylesheet")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.claims(newOutputDestinationPlan())
			if err == nil || !strings.Contains(err.Error(), "output destination conflict") {
				t.Fatalf("claims error = %v, want output destination conflict", err)
			}
		})
	}
}

func TestBuildRejectsExternalVaultInputSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		set   func(*model.SiteConfig, string)
		setup func(*testing.T, string)
	}{
		{
			name: "custom CSS",
			set: func(cfg *model.SiteConfig, external string) {
				cfg.CustomCSS = filepath.Join(external, "sentinel.css")
			},
			setup: func(t *testing.T, external string) {
				writeBuildTestFile(t, external, "sentinel.css", "sentinel")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			external := t.TempDir()
			outputPath := filepath.Join(vaultPath, "public")
			writeBuildTestFile(t, vaultPath, "notes/note.md", "# Note")
			if tt.setup != nil {
				tt.setup(t, external)
			}
			cfg := testBuildSiteConfig()
			tt.set(&cfg, external)

			_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
			if err == nil || !strings.Contains(err.Error(), "must be the fixed vault input") {
				t.Fatalf("buildWithOptions() error = %v, want fixed vault input rejection", err)
			}
			if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
			}
		})
	}
}

func TestOutputPublisherRejectsDestinationChangesDuringStaging(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	outputPath := filepath.Join(root, "site")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatal(err)
	}
	publisher, err := prepareStagedOutputPublisher(vaultPath, outputPath)
	if err != nil {
		t.Fatalf("prepareStagedOutputPublisher() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(publisher.stagingPath, "index.html"), []byte("new site"), 0o644); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(outputPath, "user.txt")
	if err := os.WriteFile(userPath, []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = publisher.Finalize(true)
	if err == nil || !strings.Contains(err.Error(), "gained content while the site was being staged") {
		t.Fatalf("publisher.Finalize(true) error = %v, want destination change rejection", err)
	}
	if got, readErr := os.ReadFile(userPath); readErr != nil || string(got) != "user content" {
		t.Fatalf("user output = %q, %v; want preserved content", got, readErr)
	}
}

func TestBuildPublishesToNestedOutputWithMissingParents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	outputPath := filepath.Join(root, "published", "nested", "site")
	writeBuildTestFile(t, vaultPath, "notes/note.md", "# Note")

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result.OutputPath != outputPath {
		t.Fatalf("result.OutputPath = %q, want %q", result.OutputPath, outputPath)
	}
	if _, err := os.Stat(filepath.Join(outputPath, "note", "index.html")); err != nil {
		t.Fatalf("os.Stat(note page) error = %v", err)
	}
}

func TestBuildRejectsOutputOwnerCollisionBeforePublication(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputParent := filepath.Join(vaultPath, "generated")
	outputPath := filepath.Join(outputParent, "public")
	writeBuildTestFile(t, vaultPath, "style.css/note.md", `---
title: Note
---
# Note
`)

	_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "output destination conflict") {
		t.Fatalf("buildWithOptions() error = %v, want output owner collision", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
	}
	if _, statErr := os.Stat(outputParent); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(output parent) error = %v, want no parent created by failed build", statErr)
	}
}
