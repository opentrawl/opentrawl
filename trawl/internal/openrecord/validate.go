package openrecord

import (
	"fmt"
	"net/url"
	"strings"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

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
	if record.Presentation == nil {
		return fmt.Errorf("presentation is missing")
	}
	for blockIndex, block := range record.Presentation.Blocks {
		resource := block.GetResource()
		if resource == nil {
			continue
		}
		if err := validateSourceRef(sourceID, resource.Ref, "resource ref"); err != nil {
			return fmt.Errorf("block %d: %w", blockIndex+1, err)
		}
	}
	for actionIndex, action := range record.Presentation.Actions {
		if action == nil {
			return fmt.Errorf("action %d is missing", actionIndex+1)
		}
		switch target := action.Target.(type) {
		case *presentationv1.Action_OpenRef:
			if err := validateSourceRef(sourceID, target.OpenRef, "action open ref"); err != nil {
				return fmt.Errorf("action %d: %w", actionIndex+1, err)
			}
		case *presentationv1.Action_Url:
			parsed, err := url.Parse(target.Url)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return fmt.Errorf("action %d: URL must use HTTPS", actionIndex+1)
			}
		default:
			return fmt.Errorf("action %d has no target", actionIndex+1)
		}
	}
	return nil
}

func validateSourceRef(sourceID, ref, field string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("%s is empty", field)
	}
	if !strings.HasPrefix(ref, sourceID+":") {
		return fmt.Errorf("%s %q is outside the %q source namespace", field, ref, sourceID)
	}
	return nil
}
