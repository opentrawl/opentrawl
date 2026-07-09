package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/avatar"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"gopkg.in/yaml.v3"
)

type RepairReport struct {
	Path              string   `json:"path"`
	Needed            bool     `json:"needed"`
	Problems          []string `json:"problems,omitempty"`
	RecoveredMetadata string   `json:"recovered_metadata,omitempty"`
	DerivedIDs        int      `json:"derived_ids,omitempty"`
}

func ReadPerson(path string) (model.Person, RepairReport, error) {
	return ReadPersonWithStableID(path, path)
}

func ReadPersonWithStableID(path, stableIDPath string) (model.Person, RepairReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Person{}, RepairReport{}, err
	}
	front, body, ok := splitFrontmatter(data)
	report := RepairReport{Path: path}
	var person model.Person
	var frontAvatar *avatarFront
	if ok {
		var parsed personFront
		if err := yaml.Unmarshal([]byte(front), &parsed); err != nil {
			report.Needed = true
			report.Problems = append(report.Problems, "invalid YAML frontmatter: "+err.Error())
			report.RecoveredMetadata = front
			person = salvagePerson(front)
		} else {
			person = personFromFrontmatter(parsed)
			frontAvatar = parsed.Avatar
		}
	} else {
		report.Needed = true
		report.Problems = append(report.Problems, "missing YAML frontmatter")
		body = string(data)
	}
	person.Body = strings.TrimLeft(body, "\n")
	person.Path = path
	report.DerivedIDs += inferPerson(&person, path, stableIDPath)
	if err := loadLegacyAvatar(&person, frontAvatar, path); err != nil {
		return model.Person{}, RepairReport{}, err
	}
	return person, report, nil
}

func ReadNote(path string) (model.Note, RepairReport, error) {
	return ReadNoteWithStableID(path, path)
}

func ReadNoteWithStableID(path, stableIDPath string) (model.Note, RepairReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Note{}, RepairReport{}, err
	}
	front, body, ok := splitFrontmatter(data)
	report := RepairReport{Path: path}
	var note model.Note
	if ok {
		if err := yaml.Unmarshal([]byte(front), &note); err != nil {
			report.Needed = true
			report.Problems = append(report.Problems, "invalid YAML frontmatter: "+err.Error())
			report.RecoveredMetadata = front
			note = salvageNote(front)
		}
	} else {
		report.Needed = true
		report.Problems = append(report.Problems, "missing YAML frontmatter")
		body = string(data)
	}
	note.Body = strings.TrimLeft(body, "\n")
	note.Path = path
	report.DerivedIDs += inferNote(&note, path, stableIDPath)
	return note, report, nil
}

func splitFrontmatter(data []byte) (string, string, bool) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", text, false
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	rest := normalized[4:]
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		if front, ok := strings.CutSuffix(rest, "\n---"); ok {
			return front, "", true
		}
		return "", text, false
	}
	return front, body, true
}

func inferPerson(person *model.Person, path, stableIDPath string) int {
	derived := 0
	if person.ID == "" {
		person.ID = stableID("person", stableIDPath)
		derived = 1
	}
	if strings.TrimSpace(person.Name) == "" {
		person.Name = nameFromBody(person.Body)
	}
	if strings.TrimSpace(person.Name) == "" {
		person.Name = strings.ReplaceAll(model.PathSlug(path), "-", " ")
	}
	if person.CreatedAt.IsZero() {
		person.CreatedAt = fileTime(path)
	}
	if person.UpdatedAt.IsZero() {
		person.UpdatedAt = fileTime(path)
	}
	if person.Accounts == nil {
		person.Accounts = map[string][]string{}
	}
	return derived
}

func inferNote(note *model.Note, path, stableIDPath string) int {
	derived := 0
	if note.ID == "" {
		note.ID = stableID("note", stableIDPath)
		derived = 1
	}
	if note.OccurredAt.IsZero() {
		note.OccurredAt = fileTime(path)
	}
	if note.CapturedAt.IsZero() {
		note.CapturedAt = fileTime(path)
	}
	if note.Kind == "" {
		note.Kind = "note"
	}
	if note.Source == "" {
		note.Source = "manual"
	}
	if note.Confidence == "" {
		note.Confidence = "medium"
	}
	if note.Privacy == "" {
		note.Privacy = "normal"
	}
	return derived
}

type personFront struct {
	ID                     string                        `yaml:"id"`
	Name                   string                        `yaml:"name"`
	SortName               string                        `yaml:"sort_name,omitempty"`
	AKA                    stringList                    `yaml:"aka,omitempty"`
	Tags                   []string                      `yaml:"tags,omitempty"`
	Emails                 []model.ContactValue          `yaml:"emails,omitempty"`
	Phones                 []model.ContactValue          `yaml:"phones,omitempty"`
	Addresses              []model.ContactValue          `yaml:"addresses,omitempty"`
	Avatar                 *avatarFront                  `yaml:"avatar,omitempty"`
	Accounts               map[string][]string           `yaml:"accounts,omitempty"`
	Sources                map[string]model.PersonSource `yaml:"sources,omitempty"`
	Apple                  *model.ExternalRef            `yaml:"apple,omitempty"`
	Google                 *model.ExternalRef            `yaml:"google,omitempty"`
	Annotation             string                        `yaml:"annotation,omitempty"`
	AnnotationStatedAt     string                        `yaml:"annotation_stated_at,omitempty"`
	LegacyAnnotationStated string                        `yaml:"annotation_stated,omitempty"`
	CreatedAt              time.Time                     `yaml:"created_at"`
	UpdatedAt              time.Time                     `yaml:"updated_at"`
}

type avatarFront struct {
	Path      string    `yaml:"path,omitempty"`
	Source    string    `yaml:"source,omitempty"`
	MIME      string    `yaml:"mime,omitempty"`
	SHA256    string    `yaml:"sha256,omitempty"`
	Width     int       `yaml:"width,omitempty"`
	Height    int       `yaml:"height,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty"`
}

func personFromFrontmatter(front personFront) model.Person {
	person := model.Person{
		ID:                 front.ID,
		Name:               front.Name,
		SortName:           front.SortName,
		AKA:                []string(front.AKA),
		Tags:               front.Tags,
		Emails:             front.Emails,
		Phones:             front.Phones,
		Addresses:          front.Addresses,
		Accounts:           front.Accounts,
		Sources:            front.Sources,
		Annotation:         front.Annotation,
		AnnotationStatedAt: firstText(front.AnnotationStatedAt, front.LegacyAnnotationStated),
		CreatedAt:          front.CreatedAt,
		UpdatedAt:          front.UpdatedAt,
	}
	if front.Avatar != nil {
		person.Avatar = model.AvatarRef{
			Source:    front.Avatar.Source,
			MIME:      front.Avatar.MIME,
			SHA256:    front.Avatar.SHA256,
			Width:     front.Avatar.Width,
			Height:    front.Avatar.Height,
			UpdatedAt: front.Avatar.UpdatedAt,
		}
	}
	if front.Apple != nil {
		person.Apple = *front.Apple
	}
	if front.Google != nil {
		person.Google = *front.Google
	}
	return person
}

func loadLegacyAvatar(person *model.Person, front *avatarFront, personPath string) error {
	if front == nil || strings.TrimSpace(front.Path) == "" {
		return nil
	}
	if filepath.IsAbs(front.Path) {
		return nil
	}
	clean := filepath.Clean(filepath.FromSlash(front.Path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil
	}
	avatarPath := filepath.Join(filepath.Dir(personPath), clean)
	data, err := os.ReadFile(avatarPath)
	if err != nil {
		return err
	}
	inspected, err := avatar.InspectBytes(data)
	if err != nil {
		return err
	}
	person.Avatar.Data = inspected.Data
	person.Avatar.SHA256 = inspected.SHA256
	person.Avatar.MIME = firstText(person.Avatar.MIME, inspected.MIME)
	person.Avatar.Source = firstText(person.Avatar.Source, "legacy")
	if person.Avatar.UpdatedAt.IsZero() {
		person.Avatar.UpdatedAt = fileTime(avatarPath)
	}
	return nil
}

func stableID(prefix, path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" || path == "." {
		path = prefix
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("opentrawl contacts legacy "+prefix+" "+path))
	return prefix + "_" + id.String()
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type stringList []string

func (list *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		item := strings.TrimSpace(value.Value)
		if item == "" {
			*list = nil
			return nil
		}
		*list = []string{item}
		return nil
	case yaml.SequenceNode:
		values := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			var text string
			if err := item.Decode(&text); err != nil {
				return err
			}
			text = strings.TrimSpace(text)
			if text != "" {
				values = append(values, text)
			}
		}
		*list = values
		return nil
	default:
		*list = nil
		return nil
	}
}

func nameFromBody(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if title, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(title)
		}
	}
	return ""
}

func fileTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Now().UTC()
	}
	return info.ModTime().UTC()
}

func salvagePerson(front string) model.Person {
	values := salvageScalars(front)
	return model.Person{
		ID:        values["id"],
		Name:      values["name"],
		SortName:  values["sort_name"],
		AKA:       splitList(values["aka"]),
		Tags:      splitList(values["tags"]),
		CreatedAt: parseTime(values["created_at"]),
		UpdatedAt: parseTime(values["updated_at"]),
	}
}

func salvageNote(front string) model.Note {
	values := salvageScalars(front)
	return model.Note{
		ID:         values["id"],
		PersonID:   values["person_id"],
		Kind:       values["kind"],
		Source:     values["source"],
		Account:    values["account"],
		ExternalID: values["external_id"],
		Direction:  values["direction"],
		Confidence: values["confidence"],
		Privacy:    values["privacy"],
		Topics:     splitList(values["topics"]),
		OccurredAt: parseTime(values["occurred_at"]),
		CapturedAt: parseTime(values["captured_at"]),
		FollowUpAt: parseTime(values["follow_up_at"]),
	}
}

func salvageScalars(front string) map[string]string {
	out := map[string]string{}
	for line := range strings.SplitSeq(front, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func splitList(value string) []string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
