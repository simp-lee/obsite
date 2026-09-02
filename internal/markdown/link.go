package markdown

import (
	"net/url"
	"path"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
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
		destination := r.rewriteDestination(strings.TrimSpace(string(link.Destination)), lineForLink(link))
		_, _ = w.WriteString(`<a href="`)
		escaped := util.URLEscape([]byte(destination), true)
		if r.Unsafe || !gmhtml.IsDangerousURL(escaped) {
			_, _ = w.Write(util.EscapeHTML(escaped))
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
	if targetPath != "" && strings.HasPrefix(targetPath, "/") {
		return raw
	}
	if targetPath != "" && !strings.HasSuffix(strings.ToLower(targetPath), ".md") && !strings.Contains(targetPath, "/") && fragment == "" {
		return raw
	}
	vaultPath := path.Clean(path.Join(path.Dir(r.sourceNote.RelPath), targetPath))
	if targetPath == "" {
		vaultPath = ""
	}
	lookup := internalwikilink.LookupTarget(r.index, r.sourceNote, vaultPath, fragment)
	if lookup.Note == nil {
		if resource := r.index.LookupResourcePath(vaultPath).Path; resource != "" {
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
		if strings.HasSuffix(strings.ToLower(targetPath), ".md") || fragment != "" {
			if r.diagnostics != nil {
				r.diagnostics.Warningf(diag.KindDeadLink, diag.Location{Path: r.sourceNote.RelPath, Line: line}, "markdown link %q could not be resolved", raw)
			}
		} else if targetPath != "" && r.diagnostics != nil {
			r.diagnostics.Warningf(diag.KindUnresolvedAsset, diag.Location{Path: r.sourceNote.RelPath, Line: line}, "markdown attachment %q could not be resolved", raw)
		}
		return raw
	}
	if lookup.Unpublished {
		if r.diagnostics != nil {
			r.diagnostics.Warningf(diag.KindDeadLink, diag.Location{Path: r.sourceNote.RelPath, Line: line}, "markdown link %q targets unpublished content", raw)
		}
		return raw
	}
	if lookup.MissingFragment {
		if r.diagnostics != nil {
			r.diagnostics.Warningf(diag.KindDeadLink, diag.Location{Path: r.sourceNote.RelPath, Line: line}, "markdown link %q targets a missing fragment", raw)
		}
		return raw
	}
	return internalwikilink.BuildNoteHref(r.outputNote, r.sourceNote, lookup.Note, lookup.FragmentID, "")
}

func lineForLink(link *gast.Link) int {
	if link == nil || link.Parent() == nil {
		return 0
	}
	return 0
}
