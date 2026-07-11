package photos

import (
	"errors"
	"fmt"
	"strings"
)

type SnapshotCompletenessState string

const (
	SnapshotComplete  SnapshotCompletenessState = "complete"
	SnapshotPartial   SnapshotCompletenessState = "partial"
	SnapshotLimited   SnapshotCompletenessState = "limited"
	SnapshotFailed    SnapshotCompletenessState = "failed"
	SnapshotCancelled SnapshotCompletenessState = "cancelled"
)

type SnapshotCompleteness struct {
	State    SnapshotCompletenessState `json:"state"`
	Evidence map[string]string         `json:"evidence"`
}

func (c SnapshotCompleteness) Validate() error {
	switch c.State {
	case SnapshotComplete, SnapshotPartial, SnapshotLimited, SnapshotFailed, SnapshotCancelled:
	default:
		return fmt.Errorf("unsupported snapshot completeness state %q", c.State)
	}
	if len(c.Evidence) == 0 {
		return errors.New("snapshot completeness provider evidence is required")
	}
	for key, value := range c.Evidence {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return errors.New("snapshot completeness provider evidence must use non-empty keys and values")
		}
	}
	return nil
}

func (c SnapshotCompleteness) Complete() bool {
	return c.State == SnapshotComplete
}
