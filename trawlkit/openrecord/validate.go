package openrecord

import (
	"fmt"
	"net/url"
	"strings"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

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

func validatePresentation(sourceID string, document *presentationv1.PresentationDocument) error {
	if document == nil {
		return fmt.Errorf("presentation is missing")
	}
	if strings.TrimSpace(document.Title) == "" {
		return fmt.Errorf("presentation title is empty")
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
	ref = strings.TrimSpace(ref)
	prefix := strings.TrimSpace(sourceID) + ":"
	if !strings.HasPrefix(ref, prefix) || strings.TrimSpace(strings.TrimPrefix(ref, prefix)) == "" {
		return fmt.Errorf("%s %q is outside the %q source namespace", field, ref, strings.TrimSpace(sourceID))
	}
	return nil
}
