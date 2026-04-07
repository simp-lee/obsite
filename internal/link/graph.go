package link

import (
	"sort"

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
