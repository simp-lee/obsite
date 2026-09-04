package markdown

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/headingid"
	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/simp-lee/obsite/internal/slug"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

type strictLinkExtender struct {
	index           *model.VaultIndex
	sourceNote      *model.Note
	outputNote      *model.Note
	headingIDPrefix string
	assetSink       AssetSink
	diagnostics     *diag.Collector
}

func (e strictLinkExtender) Extend(markdown goldmark.Markdown) {
	markdown.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&strictLinkRenderer{
		Config: gmhtml.NewConfig(), index: e.index, sourceNote: e.sourceNote, outputNote: e.outputNote, headingIDPrefix: e.headingIDPrefix, assetSink: e.assetSink, diagnostics: e.diagnostics,
	}, 499)))
}

type strictLinkRenderer struct {
	gmhtml.Config
	index           *model.VaultIndex
	sourceNote      *model.Note
	outputNote      *model.Note
	headingIDPrefix string
	assetSink       AssetSink
	diagnostics     *diag.Collector
}

func (r *strictLinkRenderer) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(gast.KindLink, r.renderLink)
}

func (r *strictLinkRenderer) renderLink(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	link := node.(*gast.Link)
	if entering {
		line := lineForLink(source, link)
		if r.sourceNote != nil && r.sourceNote.BodyStartLine > 1 {
			line += r.sourceNote.BodyStartLine - 1
		}
		destination := r.rewriteDestination(strings.TrimSpace(string(link.Destination)), line)
		_, _ = w.WriteString(`<a href="`)
		escaped := escapeDestination(destination)
		if r.Unsafe || !gmhtml.IsDangerousURL([]byte(escaped)) {
			_, _ = w.Write(util.EscapeHTML([]byte(escaped)))
		}
		_ = w.WriteByte('"')
		if len(link.Title) > 0 {
			_, _ = w.WriteString(` title="`)
			_, _ = w.Write(util.EscapeHTML(link.Title))
			_ = w.WriteByte('"')
		}
		_ = w.WriteByte('>')
		return gast.WalkContinue, nil
	}
	_, _ = w.WriteString(`</a>`)
	return gast.WalkContinue, nil
}

func (r *strictLinkRenderer) rewriteDestination(raw string, line int) string {
	if r == nil || r.index == nil || r.sourceNote == nil || raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		r.recordUnresolvedLocalAttachment(raw, line)
		return raw
	}
	if parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(raw, "//") {
		r.recordUnresolvedLocalAttachment(raw, line)
		return raw
	}
	escapedTargetPath := parsed.EscapedPath()
	targetPath, err := url.PathUnescape(escapedTargetPath)
	if err != nil {
		r.recordUnresolvedLocalAttachment(raw, line)
		return raw
	}
	fragment := parsed.Fragment
	if targetPath == "" && fragment == "" {
		return raw
	}
	rootRelative := targetPath != "" && strings.HasPrefix(targetPath, "/")
	vaultPath := path.Clean(path.Join(path.Dir(r.sourceNote.RelPath), targetPath))
	if targetPath == "" {
		vaultPath = ""
	}
	lookup := internalwikilink.LookupResult{}
	if rootRelative {
		lookup = internalwikilink.LookupRouteTarget(r.index, r.sourceNote, escapedTargetPath, fragment)
		if lookup.Note == nil && !lookup.MissingFragment {
			lookup = internalwikilink.LookupPathTarget(r.index, r.sourceNote, strings.TrimPrefix(targetPath, "/"), fragment)
		}
	} else if targetPath != "" {
		lookup = internalwikilink.LookupPathTarget(r.index, r.sourceNote, vaultPath, fragment)
		if lookup.Note == nil && r.sourceNote.Route != "" {
			if base, parseErr := url.Parse(r.sourceNote.Route); parseErr == nil {
				resolved := base.ResolveReference(parsed)
				lookup = internalwikilink.LookupRouteTarget(r.index, r.sourceNote, resolved.EscapedPath(), fragment)
			}
		}
	} else {
		lookup = internalwikilink.LookupTarget(r.index, r.sourceNote, "", fragment)
	}
	if lookup.Note == nil {
		section := lookup.Section
		if section == nil {
			section = lookupSectionTarget(r.index, r.sourceNote, targetPath)
		}
		if section != nil && inLinkVersionScope(r.sourceNote, section) {
			href := relativeToNoteOutput(r.outputNote, section.Route) + "/"
			if section.Route == "/" && !rootRelative {
				href = strings.TrimSuffix(relativeToNoteOutput(r.outputNote, "index.html"), "index.html")
			}
			if rootRelative {
				href = strings.TrimSuffix(r.outputNote.BasePath, "/") + section.Route
			}
			if parsed.RawQuery != "" || parsed.ForceQuery {
				href += "?" + parsed.RawQuery
			}
			if fragment != "" {
				if id, ok := sectionFragmentID(section, fragment); ok {
					fragment = id
				} else if r.diagnostics != nil {
					r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q targets a missing section heading", raw)})
				}
				href += "#" + fragment
			}
			return href
		}
		resourceLookup := resourcepath.LookupPath(r.sourceNote, r.index.AttachmentFolderPath, targetPath, r.index.LookupResourcePath)
		if resource := resourceLookup.Path; resource != "" {
			if resourcepath.IsResourceAllowedForNote(r.index, r.sourceNote, resource) {
				destination := resource
				if r.assetSink != nil {
					if planned := r.assetSink.Register(resource); planned != "" {
						destination = planned
					}
				}
				suffix := ""
				if parsed.RawQuery != "" {
					suffix += "?" + parsed.RawQuery
				}
				if fragment != "" {
					suffix += "#" + fragment
				}
				return relativeToNoteOutput(r.outputNote, destination) + suffix
			}
			if r.diagnostics != nil {
				r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityError, Kind: diag.KindUnresolvedAsset, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown attachment %q is outside the current version resource scope", raw)})
			}
			if rootRelative {
				return prefixRootRelativeDestination(r.outputNote, raw)
			}
			return raw
		}
		if len(resourceLookup.Ambiguous) > 0 {
			if r.diagnostics != nil {
				r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityError, Kind: diag.KindUnresolvedAsset, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown attachment %q matched multiple publishable vault assets after canonical path normalization (%s); refusing canonical fallback", raw, strings.Join(resourceLookup.Ambiguous, ", "))})
			}
			if rootRelative {
				return prefixRootRelativeDestination(r.outputNote, raw)
			}
			return raw
		}
		attachment := isMarkdownAttachmentTarget(targetPath)
		if !attachment {
			if r.diagnostics != nil {
				r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q could not be resolved", raw)})
			}
		} else if targetPath != "" && r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityError, Kind: diag.KindUnresolvedAsset, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown attachment %q could not be resolved", raw)})
		}
		if rootRelative {
			return prefixRootRelativeDestination(r.outputNote, raw)
		}
		return raw
	}
	if lookup.Unpublished {
		if r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q targets unpublished content", raw)})
		}
		return prefixRootRelativeDestination(r.outputNote, raw)
	}
	if lookup.MissingFragment {
		if r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q targets a missing fragment", raw)})
		}
		return prefixRootRelativeDestination(r.outputNote, raw)
	}
	href := internalwikilink.BuildNoteHref(r.outputNote, r.sourceNote, lookup.Note, lookup.FragmentID, r.headingIDPrefix)
	if rootRelative {
		href = strings.TrimSuffix(r.outputNote.BasePath, "/") + lookup.Note.Route
		if lookup.FragmentID != "" {
			href += "#" + lookup.FragmentID
		}
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		if fragmentIndex := strings.IndexByte(href, '#'); fragmentIndex >= 0 {
			href = href[:fragmentIndex] + "?" + parsed.RawQuery + href[fragmentIndex:]
		} else {
			href += "?" + parsed.RawQuery
		}
	}
	return href
}

func (r *strictLinkRenderer) recordUnresolvedLocalAttachment(raw string, line int) {
	if r == nil || r.diagnostics == nil || !resourcepath.IsLocalTarget(raw) {
		return
	}
	r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityError, Kind: diag.KindUnresolvedAsset, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown attachment %q could not be resolved", raw)})
}

func isMarkdownAttachmentTarget(targetPath string) bool {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(targetPath)))
	return extension != "" && extension != ".md"
}

func sectionFragmentID(section *model.Section, fragment string) (string, bool) {
	if section == nil {
		return "", false
	}
	canonical := headingid.CanonicalText(fragment)
	for _, heading := range section.Headings {
		if headingid.CanonicalText(heading.ID) == canonical || headingid.CanonicalText(heading.Text) == canonical {
			return heading.ID, heading.ID != ""
		}
	}
	return "", false
}

func lookupSectionTarget(index *model.VaultIndex, note *model.Note, target string) *model.Section {
	if index == nil {
		return nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	if strings.HasPrefix(target, "/") {
		cleaned := strings.Trim(target, "/")
		route := "/"
		if cleaned != "" {
			route = "/" + slug.EncodePath(cleaned) + "/"
		}
		return index.SectionsByRoute[route]
	}
	if note == nil {
		return nil
	}
	sectionPath := path.Clean(path.Join(path.Dir(note.RelPath), target))
	return index.Sections[sectionPath]
}

func inLinkVersionScope(note *model.Note, section *model.Section) bool {
	return note == nil || section == nil || section.VersionID == "" || note.VersionID == section.VersionID
}

func prefixRootRelativeDestination(note *model.Note, raw string) string {
	if note == nil || note.BasePath == "" || raw == "" || !strings.HasPrefix(raw, "/") {
		return raw
	}
	cut := strings.IndexAny(raw, "?#")
	if cut < 0 {
		cut = len(raw)
	}
	return strings.TrimSuffix(note.BasePath, "/") + raw[:cut] + raw[cut:]
}

func escapeDestination(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current == '%' && index+2 < len(value) && isHex(value[index+1]) && isHex(value[index+2]) {
			builder.WriteByte('%')
			builder.WriteByte(value[index+1])
			builder.WriteByte(value[index+2])
			index += 2
			continue
		}
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || strings.ContainsRune("-._~:/?#[]@!$&'()*+,;=", rune(current)) {
			builder.WriteByte(current)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hex[current>>4])
		builder.WriteByte(hex[current&0x0f])
	}
	return builder.String()
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func lineForLink(source []byte, link *gast.Link) int {
	if link == nil {
		return 0
	}
	position := link.Pos()
	if position < 0 || position > len(source) {
		return 0
	}
	return 1 + strings.Count(string(source[:position]), "\n")
}
