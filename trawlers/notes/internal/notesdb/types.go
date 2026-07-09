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
