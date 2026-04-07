package model

// VaultIndex is the immutable handoff between pass 1 indexing and pass 2 rendering.
// Pass 2 must clone any per-render note state instead of mutating notes held here.
type VaultIndex struct {
	AttachmentFolderPath string
	Notes                map[string]*Note
	NoteBySlug           map[string]*Note
	NoteByName           map[string][]*Note
	AliasByName          map[string][]*Note
	Tags                 map[string]*Tag
	Assets               map[string]*Asset
	Unpublished          UnpublishedLookup
}

// UnpublishedLookup retains enough metadata for later path/name/alias resolution
// after unpublished notes are removed from the public lookup tables.
type UnpublishedLookup struct {
	Notes       map[string]*Note
	NoteByName  map[string][]*Note
	AliasByName map[string][]*Note
}

// LinkGraph stores forward and backward note relationships.
type LinkGraph struct {
	Forward  map[string][]string
	Backward map[string][]string
}
