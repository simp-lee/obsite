package render

import (
	"bytes"
	"html/template"
	"reflect"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestThemeSlotsRenderOnceAtStableShellPositions(t *testing.T) {
	t.Parallel()

	slots := `
{{define "obsite-head-end"}}<meta data-slot="head" content="{{.Kind}}|{{.Title}}|{{.Canonical}}|{{.RelPath}}|{{.SiteRootRel}}">{{end}}
{{define "obsite-header-end"}}<span data-slot="header">{{.Site.Title}}|{{.Site.BaseURL}}</span>{{end}}
{{define "obsite-main-end"}}<img data-slot="main" src="{{themeAssetURL .SiteRootRel "icons/logo mark.svg"}}" alt="{{.Site.Description}}">{{end}}
{{define "obsite-footer-end"}}<span data-slot="footer">{{.Site.Author}}|{{.Site.Language}}</span>{{end}}
`
	site := model.SiteConfig{
		Title:       "Garden",
		BaseURL:     "https://example.com/blog/",
		Author:      "Alice",
		Description: "Field notes",
		Language:    "en",
		ThemeSlots:  slots,
	}
	tmpl, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	page := model.PageData{
		Kind:        model.PageNote,
		Site:        site,
		SiteRootRel: "../../",
		Title:       "Alpha",
		Canonical:   "https://example.com/blog/alpha/",
		RelPath:     "notes/alpha/index.html",
		Content:     template.HTML(`<p data-built-in-content>Body</p>`),
	}
	got := renderTemplate(t, tmpl, page)

	for _, slot := range []string{"head", "header", "main", "footer"} {
		if count := strings.Count(got, `data-slot="`+slot+`"`); count != 1 {
			t.Fatalf("slot %q count = %d, want 1\n%s", slot, count, got)
		}
	}
	assertOrderedStrings(t, got, `data-slot="head"`, `</head>`)
	assertOrderedStrings(t, got, `data-obsite-header`, `data-slot="header"`, `</header>`)
	assertOrderedStrings(t, got, `data-built-in-content`, `data-slot="main"`, `</main>`)
	assertOrderedStrings(t, got, `class="site-footer-note"`, `data-slot="footer"`, `</footer>`)
	for _, want := range []string{
		`content="note|Alpha|https://example.com/blog/alpha/|notes/alpha/index.html|../../"`,
		`>Garden|https://example.com/blog/</span>`,
		`src="../../assets/theme/icons/logo%20mark.svg"`,
		`alt="Field notes"`,
		`>Alice|en</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered slots missing %q\n%s", want, got)
		}
	}
}

func TestThemeSlotsInvokeEverySlotForEveryPageKind(t *testing.T) {
	t.Parallel()

	site := model.SiteConfig{Title: "Garden", Language: "en", ThemeSlots: `
{{define "obsite-head-end"}}<meta name="all-hd">{{end}}
{{define "obsite-header-end"}}<span id="all-hr"></span>{{end}}
{{define "obsite-main-end"}}<span id="all-mn"></span>{{end}}
{{define "obsite-footer-end"}}<span id="all-ft"></span>{{end}}
`}
	tmpl, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	for _, kind := range []model.PageKind{model.PageNote, model.PageIndex, model.PageTag, model.PageFolder, model.PageTimeline, model.Page404} {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			got := renderTemplate(t, tmpl, model.PageData{Kind: kind, Site: site, Title: "Page", Content: template.HTML("<p>Body</p>")})
			for _, marker := range []string{"all-hd", "all-hr", "all-mn", "all-ft"} {
				if count := strings.Count(got, marker); count != 1 {
					t.Fatalf("kind %q marker %q count = %d, want 1\n%s", kind, marker, count, got)
				}
			}
		})
	}
}

func TestThemeSlotProjectionExposesOnlyPromisedFields(t *testing.T) {
	t.Parallel()

	assertStructFields(t, reflect.TypeFor[SlotData](), []string{"Kind", "Title", "Canonical", "RelPath", "SiteRootRel", "Site"})
	assertStructFields(t, reflect.TypeFor[SlotSiteData](), []string{"Title", "BaseURL", "Author", "Description", "Language"})

	site := model.SiteConfig{Title: "Garden", Language: "en", ThemeSlots: `{{define "obsite-main-end"}}{{.Content}} {{.Site.Sidebar.Enabled}}{{end}}`}
	tmpl, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	var output bytes.Buffer
	err = tmpl.ExecuteTemplate(&output, baseTemplateName, model.PageData{Kind: model.PageIndex, Site: site, Title: "Index"})
	if err == nil || !strings.Contains(err.Error(), "can't evaluate field Content") {
		t.Fatalf("ExecuteTemplate() error = %v, want inaccessible PageData field", err)
	}
}

func TestValidateThemeSlotsRejectsAnythingOutsideFourDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "parse error", source: `{{define "obsite-head-end"}}`, want: "unexpected EOF"},
		{name: "root content", source: `outside{{define "obsite-head-end"}}inside{{end}}`, want: "only named definitions"},
		{name: "unknown definition", source: `{{define "other"}}x{{end}}`, want: `unknown theme slot definition "other"`},
		{name: "built-in redefinition", source: `{{define "base"}}x{{end}}`, want: `unknown theme slot definition "base"`},
		{name: "empty built-in redefinition", source: `{{define "base"}}{{end}}`, want: `unknown theme slot definition "base"`},
		{name: "empty definition name", source: `{{define ""}}{{end}}`, want: `unknown theme slot definition ""`},
		{name: "primary parser root definition", source: `{{define "obsite-theme-slots-primary"}}{{end}}`, want: `unknown theme slot definition "obsite-theme-slots-primary"`},
		{name: "secondary parser root definition", source: `{{define "obsite-theme-slots-secondary"}}{{end}}`, want: `unknown theme slot definition "obsite-theme-slots-secondary"`},
		{name: "extra function", source: `{{define "obsite-head-end"}}{{pageAssetURL "." "x"}}{{end}}`, want: `function "pageAssetURL" not defined`},
		{name: "template invocation", source: `{{define "obsite-main-end"}}{{template "content" .}}{{end}}`, want: `template invocation "content" is not allowed`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateThemeSlots(tt.source)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateThemeSlots() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestThemeAssetURLValidatesAndResolvesPageRelativePaths(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		root     string
		asset    string
		want     string
		wantFail bool
	}{
		{name: "root page", root: "./", asset: "icons/logo.svg", want: "./assets/theme/icons/logo.svg"},
		{name: "nested page", root: "../../", asset: "fonts/Display Face.woff2", want: "../../assets/theme/fonts/Display%20Face.woff2"},
		{name: "empty", root: "./", asset: "", wantFail: true},
		{name: "absolute", root: "./", asset: "/logo.svg", wantFail: true},
		{name: "traversal", root: "./", asset: "../logo.svg", wantFail: true},
		{name: "unclean", root: "./", asset: "icons/../logo.svg", wantFail: true},
		{name: "query", root: "./", asset: "logo.svg?v=1", wantFail: true},
		{name: "backslash", root: "./", asset: `icons\logo.svg`, wantFail: true},
		{name: "scheme", root: "./", asset: "https://example.com/logo.svg", wantFail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := themeAssetURL(tt.root, tt.asset)
			if tt.wantFail {
				if err == nil {
					t.Fatalf("themeAssetURL() = %q, nil; want error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("themeAssetURL() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func assertStructFields(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	got := make([]string, typ.NumField())
	for index := range got {
		got[index] = typ.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %#v, want %#v", typ, got, want)
	}
}

func assertOrderedStrings(t *testing.T, source string, markers ...string) {
	t.Helper()
	previous := -1
	for _, marker := range markers {
		index := strings.Index(source, marker)
		if index < 0 {
			t.Fatalf("source missing %q\n%s", marker, source)
		}
		if index <= previous {
			t.Fatalf("marker %q is out of order\n%s", marker, source)
		}
		previous = index
	}
}
