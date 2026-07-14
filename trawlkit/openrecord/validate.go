package openrecord

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

const MaximumResourceBytes uint32 = 4 << 20

func ValidHTTPSURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func Validate(record *openv1.OpenRecord) error {
	if record == nil {
		return fmt.Errorf("open record is missing")
	}
	sourceID := strings.TrimSpace(record.SourceId)
	if sourceID == "" {
		return fmt.Errorf("source id is empty")
	}
	if err := validateSourceRef(sourceID, record.OpenRef, "open ref"); err != nil {
		return err
	}
	if record.Data == nil || strings.TrimSpace(record.Data.TypeUrl) == "" {
		return fmt.Errorf("machine data is missing")
	}
	if err := validatePresentation(sourceID, record.Presentation); err != nil {
		return err
	}
	return nil
}

func ValidateRequestedAnchor(record *openv1.OpenRecord, requestedAnchorID string) error {
	if err := Validate(record); err != nil {
		return err
	}
	requestedAnchorID = strings.TrimSpace(requestedAnchorID)
	if requestedAnchorID == "" {
		return fmt.Errorf("requested anchor id is empty")
	}
	if presentationAnchorCount(record.Presentation, requestedAnchorID) != 1 {
		return fmt.Errorf("presentation does not contain requested anchor %q exactly once", requestedAnchorID)
	}
	return nil
}

func presentationAnchorCount(document *presentationv1.PresentationDocument, wanted string) int {
	count := 0
	add := func(anchorID string) {
		if anchorID == wanted {
			count++
		}
	}
	for _, block := range document.Blocks {
		if block == nil {
			continue
		}
		add(block.AnchorId)
		switch content := block.Content.(type) {
		case *presentationv1.Block_Fields:
			if content.Fields != nil {
				for _, field := range content.Fields.Fields {
					if field != nil {
						add(field.AnchorId)
					}
				}
			}
		case *presentationv1.Block_Table:
			if content.Table != nil {
				for _, row := range content.Table.Rows {
					if row != nil {
						add(row.AnchorId)
					}
				}
			}
		case *presentationv1.Block_Resource:
			if content.Resource != nil {
				add(content.Resource.AnchorId)
				for _, field := range content.Resource.Metadata {
					if field != nil {
						add(field.AnchorId)
					}
				}
			}
		}
	}
	return count
}

func ValidateResourceResponse(request *presentationv1.ResourceRequest, response *presentationv1.ResourceResponse) error {
	if err := ValidateResourceRequest(request); err != nil {
		return err
	}
	if response == nil {
		return fmt.Errorf("resource response is missing")
	}
	if strings.TrimSpace(response.ResourceRef) != strings.TrimSpace(request.ResourceRef) {
		return fmt.Errorf("resource response ref does not match request")
	}
	if !validContentType(response.ContentType) {
		return fmt.Errorf("resource content type is invalid")
	}
	if len(response.Data) == 0 {
		return fmt.Errorf("resource data is empty")
	}
	if uint64(len(response.Data)) > uint64(request.MaxBytes) || uint64(len(response.Data)) > uint64(MaximumResourceBytes) {
		return fmt.Errorf("resource data exceeds the requested bound")
	}
	return nil
}

func ValidateResourceRequest(request *presentationv1.ResourceRequest) error {
	if request == nil {
		return fmt.Errorf("resource request is missing")
	}
	sourceID := strings.TrimSpace(request.SourceId)
	if sourceID == "" {
		return fmt.Errorf("resource source id is empty")
	}
	if err := validateSourceRef(sourceID, request.ResourceRef, "resource ref"); err != nil {
		return err
	}
	if request.MaxBytes == 0 || request.MaxBytes > MaximumResourceBytes {
		return fmt.Errorf("resource byte bound must be between 1 and %d", MaximumResourceBytes)
	}
	return nil
}

func validContentType(value string) bool {
	value = strings.TrimSpace(value)
	major, minor, found := strings.Cut(value, "/")
	if !found || major == "" || minor == "" || strings.Contains(minor, "/") {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validatePresentation(sourceID string, document *presentationv1.PresentationDocument) error {
	if document == nil {
		return fmt.Errorf("presentation is missing")
	}
	if strings.TrimSpace(document.Title) == "" {
		return fmt.Errorf("presentation title is empty")
	}
	if err := validateAnchors(document); err != nil {
		return err
	}
	for index, block := range document.Blocks {
		if err := validateBlock(sourceID, block); err != nil {
			return fmt.Errorf("block %d: %w", index+1, err)
		}
	}
	for index, action := range document.Actions {
		if err := validateAction(sourceID, action); err != nil {
			return fmt.Errorf("action %d: %w", index+1, err)
		}
	}
	for index, fact := range document.Facts {
		if fact == nil {
			return fmt.Errorf("fact %d is missing", index+1)
		}
		if fact.Kind == presentationv1.Fact_KIND_UNSPECIFIED {
			return fmt.Errorf("fact %d kind is unspecified", index+1)
		}
		if strings.TrimSpace(fact.Message) == "" {
			return fmt.Errorf("fact %d message is empty", index+1)
		}
	}
	return nil
}

func validateAnchors(document *presentationv1.PresentationDocument) error {
	primary := strings.TrimSpace(document.PrimaryAnchorId)
	if !ValidAnchorID(primary) {
		return fmt.Errorf("presentation primary anchor id is invalid")
	}
	seen := map[string]struct{}{}
	primaryCount := 0
	add := func(raw string) error {
		if raw == "" {
			return nil
		}
		if raw != strings.TrimSpace(raw) || !ValidAnchorID(raw) {
			return fmt.Errorf("presentation anchor id %q is invalid", raw)
		}
		if _, exists := seen[raw]; exists {
			return fmt.Errorf("presentation anchor id %q is duplicated", raw)
		}
		seen[raw] = struct{}{}
		if raw == primary {
			primaryCount++
		}
		return nil
	}
	for _, block := range document.Blocks {
		if block == nil {
			continue
		}
		if err := add(block.AnchorId); err != nil {
			return err
		}
		switch content := block.Content.(type) {
		case *presentationv1.Block_Fields:
			if content.Fields != nil {
				for _, field := range content.Fields.Fields {
					if field != nil {
						if err := add(field.AnchorId); err != nil {
							return err
						}
					}
				}
			}
		case *presentationv1.Block_Table:
			if content.Table != nil {
				for _, row := range content.Table.Rows {
					if row != nil {
						if err := add(row.AnchorId); err != nil {
							return err
						}
					}
				}
			}
		case *presentationv1.Block_Resource:
			if content.Resource != nil {
				if err := add(content.Resource.AnchorId); err != nil {
					return err
				}
				for _, field := range content.Resource.Metadata {
					if field != nil {
						if err := add(field.AnchorId); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if primaryCount != 1 {
		return fmt.Errorf("presentation primary anchor %q occurs %d times", primary, primaryCount)
	}
	return nil
}

// ValidAnchorID reports whether value can safely identify one presentation
// target across the search and open boundaries.
func ValidAnchorID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validateBlock(sourceID string, block *presentationv1.Block) error {
	if block == nil {
		return fmt.Errorf("is missing")
	}
	switch content := block.Content.(type) {
	case *presentationv1.Block_Heading:
		if content.Heading == nil || strings.TrimSpace(content.Heading.Text) == "" {
			return fmt.Errorf("heading is empty")
		}
	case *presentationv1.Block_Prose:
		if content.Prose == nil || strings.TrimSpace(content.Prose.Text) == "" {
			return fmt.Errorf("prose is empty")
		}
	case *presentationv1.Block_Fields:
		if content.Fields == nil {
			return fmt.Errorf("field group is missing")
		}
		for index, field := range content.Fields.Fields {
			if field == nil {
				return fmt.Errorf("field %d is missing", index+1)
			}
			if strings.TrimSpace(field.Label) == "" {
				return fmt.Errorf("field %d label is empty", index+1)
			}
			if strings.TrimSpace(field.Display) == "" {
				return fmt.Errorf("field %d display is empty", index+1)
			}
		}
	case *presentationv1.Block_Table:
		if content.Table == nil {
			return fmt.Errorf("table is missing")
		}
		if len(content.Table.Columns) == 0 {
			return fmt.Errorf("table has no columns")
		}
		for index, column := range content.Table.Columns {
			if strings.TrimSpace(column) == "" {
				return fmt.Errorf("table column %d is empty", index+1)
			}
		}
		for rowIndex, row := range content.Table.Rows {
			if row == nil {
				return fmt.Errorf("table row %d is missing", rowIndex+1)
			}
			if row.Role == presentationv1.Row_ROLE_UNSPECIFIED {
				return fmt.Errorf("table row %d role is unspecified", rowIndex+1)
			}
			if len(row.Cells) != len(content.Table.Columns) {
				return fmt.Errorf("table row %d has %d cells, want %d", rowIndex+1, len(row.Cells), len(content.Table.Columns))
			}
			for cellIndex, cell := range row.Cells {
				if cell == nil {
					return fmt.Errorf("table row %d cell %d is missing", rowIndex+1, cellIndex+1)
				}
			}
		}
	case *presentationv1.Block_Resource:
		if err := validateResource(sourceID, content.Resource); err != nil {
			return err
		}
	default:
		return fmt.Errorf("content is missing or unknown")
	}
	return nil
}

func validateResource(sourceID string, resource *presentationv1.Resource) error {
	if resource == nil {
		return fmt.Errorf("resource is missing")
	}
	if resource.Kind == presentationv1.Resource_KIND_UNSPECIFIED {
		return fmt.Errorf("resource kind is unspecified")
	}
	if strings.TrimSpace(resource.Label) == "" {
		return fmt.Errorf("resource label is empty")
	}
	if err := validateSourceRef(sourceID, resource.Ref, "resource ref"); err != nil {
		return err
	}
	for index, field := range resource.Metadata {
		if field == nil {
			return fmt.Errorf("resource metadata %d is missing", index+1)
		}
		if strings.TrimSpace(field.Label) == "" || strings.TrimSpace(field.Display) == "" {
			return fmt.Errorf("resource metadata %d is empty", index+1)
		}
	}
	return nil
}

func validateAction(sourceID string, action *presentationv1.Action) error {
	if action == nil {
		return fmt.Errorf("is missing")
	}
	if strings.TrimSpace(action.Label) == "" {
		return fmt.Errorf("label is empty")
	}
	switch target := action.Target.(type) {
	case *presentationv1.Action_OpenRef:
		return validateSourceRef(sourceID, target.OpenRef, "open ref")
	case *presentationv1.Action_Url:
		if !ValidHTTPSURL(target.Url) {
			return fmt.Errorf("URL must use HTTPS")
		}
		return nil
	default:
		return fmt.Errorf("has no target")
	}
}

func validateSourceRef(sourceID, ref, field string) error {
	if !ValidSourceRef(sourceID, ref) {
		return fmt.Errorf("%s %q is outside the %q source namespace", field, ref, strings.TrimSpace(sourceID))
	}
	return nil
}

// ValidSourceRef reports whether ref is a canonical, non-empty opaque
// reference owned by sourceID.
func ValidSourceRef(sourceID, ref string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || ref != strings.TrimSpace(ref) {
		return false
	}
	prefix := sourceID + ":"
	return strings.HasPrefix(ref, prefix) && strings.TrimSpace(strings.TrimPrefix(ref, prefix)) != ""
}
