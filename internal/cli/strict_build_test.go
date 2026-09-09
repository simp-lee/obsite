package cli

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestBuildCommandPublishesVersionSwitchingAndEscapedSourceTemplates(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "public")
	writeValidateFile(t, vault, "obsite.yaml", `title: Versioned Site
baseURL: https://example.test/base/
navigation:
  - name: Docs
    section: docs
source:
  editURL: https://git.example/edit/:path?ref=main
  viewURL: https://git.example/view/:path
versions:
  root: docs
  default: v1
  entries:
    - id: v1
      label: Version 1
      source: v1
    - id: v2
      label: Version 2
      source: v2
`)
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeValidateFile(t, vault, "docs/_index.md", "---\ntitle: Docs\npublish: true\n---\n")
	for _, version := range []string{"v1", "v2"} {
		writeValidateFile(t, vault, "docs/"+version+"/_index.md", "---\ntitle: "+version+"\npublish: true\n---\n")
	}
	writeValidateFile(t, vault, "docs/v1/Start Here.md", "---\ntitle: Start V1\npublish: true\ntype: doc\nslug: start-v1\n---\n")
	writeValidateFile(t, vault, "docs/v2/Start Here.md", "---\ntitle: Start V2\npublish: true\ntype: doc\nslug: start-v2\n---\n")

	_, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--strict", "--vault", vault, "--output", output})
	if err != nil {
		t.Fatalf("build --strict error = %v; stderr=%q", err, stderr)
	}
	v1, err := os.ReadFile(filepath.Join(output, "docs", "v1", "start-v1", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class=version-selector`,
		`https://git.example/edit/docs/v1/Start%20Here.md?ref=main`,
		`https://git.example/view/docs/v1/Start%20Here.md`,
		`href=/base/docs/v2/start-v2/`,
	} {
		if !bytes.Contains(v1, []byte(want)) {
			t.Fatalf("versioned page missing %q: %s", want, v1)
		}
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

func TestBuildCommandPreservesPublishedOutputWhenSocialGenerationFails(t *testing.T) {
	vault := t.TempDir()
	outputRoot := t.TempDir()
	output := filepath.Join(outputRoot, "public")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\ncover: images/cover.png\n---\n")
	var validCover bytes.Buffer
	if err := png.Encode(&validCover, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	writeValidateFile(t, vault, "images/cover.png", validCover.String())
	if _, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output}); err != nil {
		t.Fatalf("initial build error = %v; stderr=%q", err, stderr)
	}
	before := snapshotCLIOutput(t, output)

	// The real pipeline decodes a cover during analysis, staged-asset planning,
	// and the final cover read. This stateful test decoder lets those checks pass
	// and then fails inside social.Generate, without replacing the CLI's real
	// build dependency with a synthetic callback.
	magic := "OBSITE-CLI-SOCIAL-FAILURE:" + vault + ":"
	injectedFailure := errors.New("injected social image decode failure")
	var decodeCalls atomic.Int32
	image.RegisterFormat("png", magic, func(io.Reader) (image.Image, error) {
		if decodeCalls.Add(1) > 3 {
			return nil, injectedFailure
		}
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
	}, func(io.Reader) (image.Config, error) {
		return image.Config{ColorModel: color.RGBAModel, Width: 2, Height: 2}, nil
	})
	writeValidateFile(t, vault, "images/cover.png", magic)

	_, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output})
	if err == nil || !strings.Contains(err.Error(), "generate social card") || !strings.Contains(err.Error(), injectedFailure.Error()) {
		t.Fatalf("build error = %v; stderr=%q, want social generation failure", err, stderr)
	}
	assertCLIOutputUnchanged(t, output, before, "social generation failure")
	assertNoCLITransactionResidue(t, outputRoot, output)
}

func TestCLIRejectsStrictInputFailuresWithSharedDiagnosticsAndPreservesOutput(t *testing.T) {
	type expectedDiagnostic struct {
		kind       diag.Kind
		pathSuffix string
		line       int
		field      string
		target     string
		message    string
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, vault string)
		want   expectedDiagnostic
	}{
		{
			name: "duplicate configuration key",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\ntitle: Other\n")
			},
			want: expectedDiagnostic{kind: diag.KindSchema, pathSuffix: "obsite.yaml", line: 4, field: "title", message: `duplicate key "title"`},
		},
		{
			name: "null frontmatter field",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "article.md", "---\ntitle: null\npublish: true\ntype: page\n---\n")
			},
			want: expectedDiagnostic{kind: diag.KindSchema, pathSuffix: "article.md", line: 2, field: "title", message: "must not be null"},
		},
		{
			name: "invalid source template",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\nsource:\n  editURL: https://git.example/edit/:path/:branch\n")
			},
			want: expectedDiagnostic{kind: diag.KindSchema, pathSuffix: "obsite.yaml", line: 5, field: "source.editURL", message: `unknown template placeholder ":branch"`},
		},
		{
			name: "invalid version source",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "obsite.yaml", `title: Site
baseURL: https://example.test/
navigation: []
versions:
  root: docs
  default: v1
  entries:
    - id: v1
      label: Version 1
      source: missing
`)
			},
			want: expectedDiagnostic{kind: diag.KindVersion, pathSuffix: "docs/missing/_index.md", field: "versions", message: `version source "docs/missing" must contain _index.md`},
		},
		{
			name: "invalid article metadata",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\nstatus: draft\n---\n")
			},
			want: expectedDiagnostic{kind: diag.KindSchema, pathSuffix: "article.md", line: 5, field: "status", message: "must be stable, experimental, or deprecated"},
		},
		{
			name: "undecodable cover",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\ncover: images/cover.png\n---\n")
				writeValidateFile(t, vault, "images/cover.png", "not an image")
			},
			want: expectedDiagnostic{kind: diag.KindMetadata, pathSuffix: "article.md", line: 5, field: "cover", target: "images/cover.png", message: "cover cannot be decoded"},
		},
		{
			name: "external banner resource",
			mutate: func(t *testing.T, vault string) {
				writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\nbanner: images/banner.svg\nbannerAlt: External\n---\n")
				writeValidateFile(t, vault, "images/banner.svg", `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://example.test/banner.png"/></svg>`)
			},
			want: expectedDiagnostic{kind: diag.KindMetadata, pathSuffix: "article.md", line: 5, field: "banner", target: "images/banner.svg", message: "external SVG reference"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vault := t.TempDir()
			outputRoot := t.TempDir()
			output := filepath.Join(outputRoot, "public")
			writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
			writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nPublished baseline\n")
			if _, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--strict", "--vault", vault, "--output", output}); err != nil {
				t.Fatalf("baseline build error = %v; stderr=%q", err, stderr)
			}
			before := snapshotCLIOutput(t, output)
			test.mutate(t, vault)

			stdout, validateStderr, validateErr := executeForTest(t, defaultCommandDependencies(), []string{"validate", "--vault", vault})
			if validateErr == nil {
				t.Fatal("validate error = nil, want strict input failure")
			}
			if stdout != "" {
				t.Fatalf("validate stdout = %q, want empty", stdout)
			}
			validateDiagnostics := parseCLIDiagnostics(t, validateStderr)
			assertExpectedCLIDiagnostic(t, validateDiagnostics, test.want.kind, test.want.pathSuffix, test.want.line, test.want.field, test.want.target, test.want.message)
			assertCLIOutputUnchanged(t, output, before, "validate failure")

			stdout, buildStderr, buildErr := executeForTest(t, defaultCommandDependencies(), []string{"build", "--strict", "--vault", vault, "--output", output})
			if buildErr == nil {
				t.Fatal("build --strict error = nil, want strict input failure")
			}
			if stdout != "" {
				t.Fatalf("build --strict stdout = %q, want empty", stdout)
			}
			buildDiagnostics := parseCLIDiagnostics(t, buildStderr)
			if !reflect.DeepEqual(buildDiagnostics, validateDiagnostics) {
				t.Fatalf("build diagnostics = %#v, want validate diagnostics %#v", buildDiagnostics, validateDiagnostics)
			}
			assertCLIOutputUnchanged(t, output, before, "build --strict failure")
			assertNoCLITransactionResidue(t, outputRoot, output)
		})
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

func assertExpectedCLIDiagnostic(t *testing.T, diagnostics []diag.Diagnostic, kind diag.Kind, pathSuffix string, line int, field, target, message string) {
	t.Helper()
	for _, item := range diagnostics {
		if item.Severity != diag.SeverityError || item.Kind != kind || item.Location.Line != line || item.Field != field || item.Target != target {
			continue
		}
		if !strings.HasSuffix(filepath.ToSlash(item.Location.Path), filepath.ToSlash(pathSuffix)) || !strings.Contains(item.Message, message) {
			continue
		}
		return
	}
	t.Fatalf("diagnostics = %#v, want error %s at %s:%d field=%q target=%q containing %q", diagnostics, kind, pathSuffix, line, field, target, message)
}

func snapshotCLIOutput(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	if err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	}); err != nil {
		t.Fatalf("snapshot output %q: %v", root, err)
	}
	return files
}

func assertCLIOutputUnchanged(t *testing.T, root string, before map[string][]byte, operation string) {
	t.Helper()
	after := snapshotCLIOutput(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("published output changed after %s", operation)
	}
}

func assertNoCLITransactionResidue(t *testing.T, parent, output string) {
	t.Helper()
	prefix := "." + filepath.Base(output) + "-obsite-"
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			t.Fatalf("transaction residue after CLI failure: %s", filepath.Join(parent, entry.Name()))
		}
	}
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
