package markdown

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

type strictLinkExtender struct {
	index       *model.VaultIndex
	sourceNote  *model.Note
	outputNote  *model.Note
	assetSink   AssetSink
	diagnostics *diag.Collector
}

func (e strictLinkExtender) Extend(markdown goldmark.Markdown) {
	markdown.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&strictLinkRenderer{
		Config: gmhtml.NewConfig(), index: e.index, sourceNote: e.sourceNote, outputNote: e.outputNote, assetSink: e.assetSink, diagnostics: e.diagnostics,
	}, 499)))
}

type strictLinkRenderer struct {
	gmhtml.Config
	index       *model.VaultIndex
	sourceNote  *model.Note
	outputNote  *model.Note
	assetSink   AssetSink
	diagnostics *diag.Collector
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
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(raw, "//") {
		return raw
	}
	targetPath, err := url.PathUnescape(parsed.Path)
	if err != nil {
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
	if rootRelative && strings.HasSuffix(strings.ToLower(targetPath), ".md") {
		lookup = internalwikilink.LookupPathTarget(r.index, r.sourceNote, strings.TrimPrefix(targetPath, "/"), fragment)
	} else if !rootRelative && (strings.HasSuffix(strings.ToLower(targetPath), ".md") || strings.Contains(targetPath, "/")) {
		lookup = internalwikilink.LookupPathTarget(r.index, r.sourceNote, vaultPath, fragment)
	} else if !rootRelative {
		lookup = internalwikilink.LookupTarget(r.index, r.sourceNote, vaultPath, fragment)
	}
	if lookup.Note == nil {
		if resource := resourcepath.LookupPath(r.sourceNote, r.index.AttachmentFolderPath, targetPath, r.index.LookupResourcePath).Path; resource != "" {
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
		if rootRelative {
			return basePathDestination(r.outputNote, raw)
		}
		if strings.HasSuffix(strings.ToLower(targetPath), ".md") || fragment != "" {
			if r.diagnostics != nil {
				r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q could not be resolved", raw)})
			}
		} else if targetPath != "" && r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindUnresolvedAsset, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown attachment %q could not be resolved", raw)})
		}
		return raw
	}
	if lookup.Unpublished {
		if r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q targets unpublished content", raw)})
		}
		return raw
	}
	if lookup.MissingFragment {
		if r.diagnostics != nil {
			r.diagnostics.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindDeadLink, Location: diag.Location{Path: r.sourceNote.RelPath, Line: line}, Target: raw, Message: fmt.Sprintf("markdown link %q targets a missing fragment", raw)})
		}
		return raw
	}
	return internalwikilink.BuildNoteHref(r.outputNote, r.sourceNote, lookup.Note, lookup.FragmentID, "")
}

func basePathDestination(note *model.Note, raw string) string {
	if note == nil || note.BasePath == "" || raw == "" || !strings.HasPrefix(raw, "/") {
		return raw
	}
	return strings.TrimSuffix(note.BasePath, "/") + raw
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
