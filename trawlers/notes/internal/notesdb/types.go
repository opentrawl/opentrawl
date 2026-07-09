package notesdb

type Snapshot struct {
	SourcePath string
	Path       string
	root       string
}

type Note struct {
	ID         string
	Title      string
	Folder     string
	CreatedAt  string
	ModifiedAt string
	// PasswordProtected and NeedsInitialFetch explain why a note carries no
	// readable body. Apple locks an encrypted note's plaintext (password
	// protected) and leaves a placeholder row for a note whose content has not
	// yet downloaded from iCloud (needs initial fetch). Sync reports these
	// instead of archiving an empty row (engineering rules 1.15).
	PasswordProtected bool
	NeedsInitialFetch bool
}

type Body struct {
	NoteID           string
	SourceModifiedAt string
	ZData            []byte
}

type FinalState struct {
	Notes  []Note
	Bodies []Body
}

// Attachment is one row from ZICCLOUDSYNCINGOBJECT where ZTYPEUTI is not
// null. HasMedia says whether the source row references a media row (ZMEDIA
// is set); gallery containers, tables, paper, scans and URL attachments have
// no media and that is normal. MediaID is the referenced media row's UUID --
// empty while HasMedia is true means the reference dangles (the media row is
// gone), which is corruption, not a file-less attachment.
type Attachment struct {
	ID       string
	NoteID   string
	Name     string
	Type     string
	HasMedia bool
	MediaID  string
}
