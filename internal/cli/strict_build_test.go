package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
)

func TestStrictBuildPublishesSectionAndArticlePages(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "public")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation:\n  - name: Home\n    section: .\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nWelcome\n")
	writeValidateFile(t, vault, "guide/_index.md", "---\ntitle: Guide\npublish: true\n---\nGuide body\n")
	writeValidateFile(t, vault, "guide/01-start.md", "---\ntitle: Start\npublish: true\ntype: doc\n---\nStart body\n")
	_, _, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output})
	if err != nil {
		t.Fatalf("strict build error = %v", err)
	}
	for _, rel := range []string{"index.html", "guide/index.html", "guide/start/index.html", "style.css"} {
		if _, statErr := os.Stat(filepath.Join(output, filepath.FromSlash(rel))); statErr != nil {
			t.Fatalf("missing output %q: %v", rel, statErr)
		}
	}
	data, err := os.ReadFile(filepath.Join(output, "guide", "start", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Document navigation`) || !strings.Contains(string(data), "Start body") {
		t.Fatalf("article output = %s", data)
	}
}

func TestStrictBuildPublishesBannersAndIndependentSocialCards(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "public")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\ndefaultImg: images/cover.png\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\nbanner: images/banner.png\nbannerAlt: Home banner\n---\n")
	writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\nbanner: images/banner.png\nbannerAlt: Article banner\ncover: images/cover.png\n---\nArticle\n")
	for _, name := range []string{"banner.png", "cover.png"} {
		var data bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		writeValidateFile(t, vault, "images/"+name, data.String())
	}
	_, _, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output})
	if err != nil {
		t.Fatal(err)
	}
	article, err := os.ReadFile(filepath.Join(output, "article", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(article), `alt="Article banner"`) || !strings.Contains(string(article), `og:image`) {
		t.Fatalf("article output = %s", article)
	}
	entries, err := filepath.Glob(filepath.Join(output, "assets", "social", "*", "*.png"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("social outputs = %v, err=%v", entries, err)
	}
	bannerEntries, err := filepath.Glob(filepath.Join(output, "assets", "banner.*.png"))
	if err != nil || len(bannerEntries) != 1 {
		t.Fatalf("content-addressed banner outputs = %v, err=%v", bannerEntries, err)
	}
}

func TestValidateNormalAndStrictBuildShareWarningDiagnostics(t *testing.T) {
	vault := t.TempDir()
	normalOutput := filepath.Join(t.TempDir(), "normal")
	strictOutput := filepath.Join(t.TempDir(), "strict")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeValidateFile(t, vault, "broken.md", "---\ntitle: Broken\npublish: true\ntype: page\n---\nSee [[Missing]].\n")

	_, validateStderr, validateErr := executeForTest(t, defaultCommandDependencies(), []string{"validate", "--vault", vault})
	if validateErr == nil {
		t.Fatal("validate error = nil, want warning failure")
	}
	_, normalStderr, normalErr := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", normalOutput})
	if normalErr != nil {
		t.Fatalf("normal build error = %v", normalErr)
	}
	_, strictStderr, strictErr := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", strictOutput, "--strict"})
	if strictErr == nil || !strings.Contains(strictErr.Error(), "warning") {
		t.Fatalf("strict build error = %v, want warning failure", strictErr)
	}

	got := map[string][]diag.Diagnostic{
		"validate": parseCLIDiagnostics(t, validateStderr),
		"normal":   parseCLIDiagnostics(t, normalStderr),
		"strict":   parseCLIDiagnostics(t, strictStderr),
	}
	want := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindDeadLink,
		Location: diag.Location{Path: "broken.md", Line: 6},
		Target:   "Missing",
		Message:  `wikilink "Missing" could not be resolved`,
	}}
	for command, diagnostics := range got {
		if !reflect.DeepEqual(diagnostics, want) {
			t.Errorf("%s diagnostics = %#v, want %#v", command, diagnostics, want)
		}
	}
	if !reflect.DeepEqual(got["validate"], got["normal"]) || !reflect.DeepEqual(got["validate"], got["strict"]) {
		t.Fatalf("commands did not share normalized diagnostics: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(normalOutput, "broken", "index.html")); err != nil {
		t.Fatalf("normal build did not publish output: %v", err)
	}
	if _, err := os.Stat(strictOutput); !os.IsNotExist(err) {
		t.Fatalf("strict warning changed output: %v", err)
	}
}

// parseCLIDiagnostics decodes the stable diagnostic lines emitted by the CLI so
// this test compares the structured contract rather than just rendered text.
func parseCLIDiagnostics(t *testing.T, stderr string) []diag.Diagnostic {
	t.Helper()
	var diagnostics []diag.Diagnostic
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "" {
			continue
		}
		header, message, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("diagnostic line %q has no message separator", line)
		}
		parts := strings.SplitN(header, " ", 3)
		if len(parts) != 3 {
			t.Fatalf("diagnostic line %q has malformed header", line)
		}
		location := parts[2]
		item := diag.Diagnostic{Severity: diag.Severity(parts[0]), Kind: diag.Kind(parts[1]), Message: message}
		for {
			start := strings.LastIndex(location, " [")
			if start < 0 || !strings.HasSuffix(location, "]") {
				break
			}
			field := location[start+2 : len(location)-1]
			switch {
			case strings.HasPrefix(field, "field="):
				item.Field = strings.TrimPrefix(field, "field=")
			case strings.HasPrefix(field, "target="):
				item.Target = strings.TrimPrefix(field, "target=")
			}
			location = location[:start]
		}
		lastColon := strings.LastIndex(location, ":")
		if lastColon >= 0 {
			if lineNumber, err := strconv.Atoi(location[lastColon+1:]); err == nil {
				item.Location.Line = lineNumber
				location = location[:lastColon]
			}
		}
		item.Location.Path = location
		diagnostics = append(diagnostics, item)
	}
	return diagnostics
}

func TestStrictBuildStopsBeforePublicationOnSchemaFailure(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "public")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\ndefaultPublish: true\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	_, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output, "--strict"})
	if err == nil || !strings.Contains(err.Error(), "defaultPublish") {
		t.Fatalf("strict build error=%v stderr=%q", err, stderr)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("strict failure changed output: %v", statErr)
	}
}
