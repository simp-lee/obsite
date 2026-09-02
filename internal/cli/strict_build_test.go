package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := os.Stat(filepath.Join(output, "assets", "banner.png")); err != nil {
		t.Fatal(err)
	}
}

func TestStrictAndNormalBuildApplyWarningPolicyToTheSameAnalysis(t *testing.T) {
	vault := t.TempDir()
	normalOutput := filepath.Join(t.TempDir(), "normal")
	strictOutput := filepath.Join(t.TempDir(), "strict")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeValidateFile(t, vault, "broken.md", "---\ntitle: Broken\npublish: true\ntype: page\n---\nSee [[Missing]].\n")
	_, normalStderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", normalOutput})
	if err != nil {
		t.Fatalf("normal build error = %v", err)
	}
	if !strings.Contains(normalStderr, "deadlink") {
		t.Fatalf("normal diagnostics = %q, want deadlink warning", normalStderr)
	}
	if _, err := os.Stat(filepath.Join(normalOutput, "broken", "index.html")); err != nil {
		t.Fatalf("normal build did not publish output: %v", err)
	}
	_, strictStderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", strictOutput, "--strict"})
	if err == nil || !strings.Contains(err.Error(), "warning") {
		t.Fatalf("strict build error = %v, want warning failure", err)
	}
	if !strings.Contains(strictStderr, "deadlink") {
		t.Fatalf("strict diagnostics = %q, want same deadlink category", strictStderr)
	}
	if _, err := os.Stat(strictOutput); !os.IsNotExist(err) {
		t.Fatalf("strict warning changed output: %v", err)
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
