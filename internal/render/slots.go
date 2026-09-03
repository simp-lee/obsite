package render

import (
	"bytes"
	"fmt"
	"html/template"
	"path"
	"strings"

	textparse "text/template/parse"

	"github.com/simp-lee/obsite/internal/slug"
)

const (
	themeSlotsRootName           = "obsite-theme-slots-primary"
	themeSlotsValidationRootName = "obsite-theme-slots-secondary"
)

var themeSlotNames = [...]string{
	"obsite-head-end",
	"obsite-header-end",
	"obsite-main-end",
	"obsite-footer-end",
}

// SlotSiteData is the complete site projection available to theme slots.
type SlotSiteData struct {
	Title       string
	BaseURL     string
	Author      string
	Description string
	Language    string
}

// SlotData is the complete page projection available to theme slots.
type SlotData struct {
	Kind        string
	Title       string
	Canonical   string
	RelPath     string
	SiteRootRel string
	Site        SlotSiteData
}

// ValidateThemeSlots parses and validates one append-only slots.html snapshot.
func ValidateThemeSlots(source string) error {
	_, err := parseThemeSlots(source)
	return err
}

// RenderThemeSlots executes each supported append-only slot once.
func RenderThemeSlots(source string, data SlotData) (map[string]string, error) {
	result := make(map[string]string, len(themeSlotNames))
	if strings.TrimSpace(source) == "" {
		return result, nil
	}
	slots, err := parseThemeSlots(source)
	if err != nil {
		return nil, err
	}
	for _, name := range themeSlotNames {
		if slots.Lookup(name) == nil {
			continue
		}
		var output bytes.Buffer
		if err := slots.ExecuteTemplate(&output, name, data); err != nil {
			return nil, fmt.Errorf("render theme slot %q: %w", name, err)
		}
		result[name] = output.String()
	}
	return result, nil
}

func parseThemeSlots(source string) (*template.Template, error) {
	funcs := template.FuncMap{"themeAssetURL": themeAssetURL}
	slots, err := template.New(themeSlotsRootName).Funcs(funcs).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse theme slots: %w", err)
	}
	if err := validateThemeSlotDefinitions(slots, themeSlotsRootName); err != nil {
		return nil, err
	}
	return slots, nil
}

func validateThemeSlotDefinitions(slots *template.Template, rootName string) error {
	root := slots.Lookup(rootName)
	if root != nil && root.Tree != nil && root.Tree.Root != nil && strings.TrimSpace(root.Tree.Root.String()) != "" {
		return fmt.Errorf("theme slots may contain only named definitions")
	}

	allowed := make(map[string]struct{}, len(themeSlotNames))
	for _, name := range themeSlotNames {
		allowed[name] = struct{}{}
	}
	for _, candidate := range slots.Templates() {
		name := candidate.Name()
		if name == rootName {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("unknown theme slot definition %q", name)
		}
		if candidate.Tree != nil {
			if err := rejectThemeSlotTemplateCalls(candidate.Tree.Root); err != nil {
				return fmt.Errorf("theme slot %q: %w", name, err)
			}
		}
	}
	return nil
}

func rejectThemeSlotTemplateCalls(node textparse.Node) error {
	if node == nil {
		return nil
	}
	switch current := node.(type) {
	case *textparse.ListNode:
		for _, child := range current.Nodes {
			if err := rejectThemeSlotTemplateCalls(child); err != nil {
				return err
			}
		}
	case *textparse.IfNode:
		if err := rejectThemeSlotTemplateCalls(current.List); err != nil {
			return err
		}
		return rejectThemeSlotTemplateCalls(current.ElseList)
	case *textparse.RangeNode:
		if err := rejectThemeSlotTemplateCalls(current.List); err != nil {
			return err
		}
		return rejectThemeSlotTemplateCalls(current.ElseList)
	case *textparse.WithNode:
		if err := rejectThemeSlotTemplateCalls(current.List); err != nil {
			return err
		}
		return rejectThemeSlotTemplateCalls(current.ElseList)
	case *textparse.TemplateNode:
		return fmt.Errorf("template invocation %q is not allowed", current.Name)
	}
	return nil
}

func themeAssetURL(siteRootRel string, assetPath string) (string, error) {
	raw := strings.TrimSpace(assetPath)
	if raw == "" || strings.Contains(raw, `\`) || strings.ContainsAny(raw, "?#:\r\n\x00") || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("theme asset path %q must be a theme-assets-relative URL path", assetPath)
	}
	cleaned := path.Clean(raw)
	if cleaned != raw || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("theme asset path %q must be a clean theme-assets-relative URL path", assetPath)
	}

	encoded := slug.EncodePath(cleaned)
	base := strings.TrimSpace(siteRootRel)
	if base == "" {
		base = "./"
	} else if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + "assets/theme/" + encoded, nil
}
