// Package resourcepath centralizes shared resource target rules.
package resourcepath

import (
	"strings"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	"github.com/simp-lee/obsite/internal/model"
)

// LookupResult describes a shared resource lookup attempt.
type LookupResult = model.PathLookupResult

// LookupPath applies the shared note-relative and attachment-folder lookup rules
// against a caller-provided lookup that can surface canonical ambiguity.
func LookupPath(note *model.Note, attachmentFolderPath string, rawTarget string, lookup func(string) model.PathLookupResult) model.PathLookupResult {
	if lookup == nil {
		return model.PathLookupResult{}
	}

	ambiguous := model.PathLookupResult{}
	for _, candidate := range CandidatePathsWithAttachmentFolder(note, attachmentFolderPath, rawTarget) {
		result := lookup(candidate)
		if strings.TrimSpace(result.Path) != "" {
			return result
		}
		if len(ambiguous.Ambiguous) == 0 && len(result.Ambiguous) > 0 {
			ambiguous = result
		}
	}

	return ambiguous
}

// ResolveIndexedAssetPath applies the indexed vault asset lookup rules.
func ResolveIndexedAssetPath(note *model.Note, idx *model.VaultIndex, rawTarget string) string {
	return LookupIndexedAssetPath(note, idx, rawTarget).Path
}

// LookupIndexedAssetPath applies the indexed vault asset lookup rules and can
// surface canonical ambiguity when fallback is refused.
func LookupIndexedAssetPath(note *model.Note, idx *model.VaultIndex, rawTarget string) model.PathLookupResult {
	if idx == nil {
		return model.PathLookupResult{}
	}

	return LookupPath(note, idx.AttachmentFolderPath, rawTarget, idx.LookupAssetPath)
}

// CandidatePathsWithAttachmentFolder expands a resource target through the shared
// note-relative and attachment-folder rules.
func CandidatePathsWithAttachmentFolder(note *model.Note, attachmentFolderPath string, rawTarget string) []string {
	return internalasset.CandidatePaths(note, attachmentFolderPath, rawTarget)
}

// LooksLikeImage reports whether a target resolves to a supported image type.
func LooksLikeImage(rawTarget string) bool {
	return internalasset.HasImageExtension(rawTarget)
}
