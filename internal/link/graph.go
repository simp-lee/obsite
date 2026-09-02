package link

import (
	"path"
	"sort"
	"strings"

	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
)

// BuildGraph derives a deterministic public note link graph from pass-2 resolved outlinks.
//
// The resolvedOutLinks map must be keyed by public source note path and contain the
// render-local outlinks produced during pass 2, including links merged onto the host
// page from embedded content. Link diagnostics are owned by resolver/render phases,
// so unresolved, self-referential, or non-public targets are ignored here.
func BuildGraph(idx *model.VaultIndex, resolvedOutLinks map[string][]model.LinkRef) *model.LinkGraph {
	graph := &model.LinkGraph{
		Forward:  map[string][]string{},
		Backward: map[string][]string{},
	}
	if idx == nil || len(idx.Notes) == 0 {
		return graph
	}

	publicPaths := sortedPublicPaths(idx.Notes)
	publicSet := make(map[string]struct{}, len(publicPaths))
	backwardSets := make(map[string]map[string]struct{}, len(publicPaths))
	for _, relPath := range publicPaths {
		graph.Forward[relPath] = []string{}
		graph.Backward[relPath] = []string{}
		publicSet[relPath] = struct{}{}
		backwardSets[relPath] = make(map[string]struct{})
	}

	for _, sourceRelPath := range publicPaths {
		outgoing := make(map[string]struct{})
		for i := range resolvedOutLinks[sourceRelPath] {
			targetRelPath := resolvedOutLinks[sourceRelPath][i].ResolvedRelPath
			if targetRelPath == "" {
				continue
			}
			if targetRelPath == sourceRelPath {
				continue
			}

			if _, ok := publicSet[targetRelPath]; !ok {
				continue
			}

			outgoing[targetRelPath] = struct{}{}
			backwardSets[targetRelPath][sourceRelPath] = struct{}{}
		}

		graph.Forward[sourceRelPath] = sortedMembers(outgoing)
	}

	for _, targetRelPath := range publicPaths {
		graph.Backward[targetRelPath] = sortedMembers(backwardSets[targetRelPath])
	}

	return graph
}

// BuildSourceGraph resolves only links and note embeds declared directly in
// each public note's source AST. It is independent from render-expanded links.
func BuildSourceGraph(idx *model.VaultIndex) *model.LinkGraph {
	graph := &model.LinkGraph{
		Forward:  map[string][]string{},
		Backward: map[string][]string{},
	}
	if idx == nil || len(idx.Notes) == 0 {
		return graph
	}

	publicPaths := sortedPublicPaths(idx.Notes)
	backwardSets := make(map[string]map[string]struct{}, len(publicPaths))
	for _, relPath := range publicPaths {
		graph.Forward[relPath] = []string{}
		graph.Backward[relPath] = []string{}
		backwardSets[relPath] = make(map[string]struct{})
	}

	for _, sourcePath := range publicPaths {
		source := idx.Notes[sourcePath]
		outgoing := make(map[string]struct{})
		if source != nil {
			for _, ref := range source.OutLinks {
				target, fragment := sourceLinkTarget(ref.RawTarget, ref.Fragment)
				addResolvedSourceTarget(outgoing, idx, source, target, fragment)
			}
			for _, embed := range source.Embeds {
				if embed.IsImage {
					continue
				}
				fragment := embed.Fragment
				if strings.HasPrefix(strings.TrimSpace(fragment), "^") {
					fragment = ""
				}
				addResolvedSourceTarget(outgoing, idx, source, embed.Target, fragment)
			}
		}

		delete(outgoing, sourcePath)
		graph.Forward[sourcePath] = sortedMembers(outgoing)
		for targetPath := range outgoing {
			backwardSets[targetPath][sourcePath] = struct{}{}
		}
	}

	for _, targetPath := range publicPaths {
		graph.Backward[targetPath] = sortedMembers(backwardSets[targetPath])
	}
	return graph
}

func addResolvedSourceTarget(targets map[string]struct{}, idx *model.VaultIndex, source *model.Note, target string, fragment string) {
	if targets == nil || idx == nil || source == nil {
		return
	}
	target = strings.TrimSpace(target)
	if target != "" && (strings.HasSuffix(strings.ToLower(target), ".md") || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../")) {
		target = path.Join(path.Dir(source.RelPath), target)
	}
	lookup := internalwikilink.LookupTarget(idx, source, target, strings.TrimSpace(fragment))
	if lookup.Note == nil || lookup.Unpublished || lookup.MissingFragment {
		return
	}
	if idx.Notes[lookup.Note.RelPath] == nil {
		return
	}
	targets[lookup.Note.RelPath] = struct{}{}
}

func sourceLinkTarget(rawTarget string, fragment string) (string, string) {
	rawTarget = strings.TrimSpace(rawTarget)
	fragment = strings.TrimSpace(fragment)
	if fragment != "" {
		if target, ok := strings.CutSuffix(rawTarget, "#"+fragment); ok {
			return strings.TrimSpace(target), fragment
		}
		return rawTarget, fragment
	}
	if target, parsedFragment, ok := strings.Cut(rawTarget, "#"); ok {
		return strings.TrimSpace(target), strings.TrimSpace(parsedFragment)
	}
	return rawTarget, ""
}

func sortedPublicPaths(notes map[string]*model.Note) []string {
	paths := make([]string, 0, len(notes))
	for relPath := range notes {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func sortedMembers(values map[string]struct{}) []string {
	if len(values) == 0 {
		return []string{}
	}

	members := make([]string, 0, len(values))
	for value := range values {
		members = append(members, value)
	}
	sort.Strings(members)
	return members
}
