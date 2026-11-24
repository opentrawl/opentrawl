package photocard

import (
	"context"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// CardModelClient sends one picture and its card model input to a hosted
// vision model and returns the model's plain-text answer.
type CardModelClient interface {
	CardModelProvider() photo.CardModelProvider
	CardModelName() string
	CardModelEffort() string
	AnswerPhotoCardModelInput(ctx context.Context, photoCardModelInputText string, pictureJPEGFilePath string) (CardModelAnswer, error)
}

type CardModelAnswer struct {
	RawModelAnswer string
	// Unset when the provider does not report them.
	InputTokens, OutputTokens, ReasoningTokens *int64
}

// CardModelCallFailedError is a failure of one call that may pass when the
// same photo is tried again later: a timeout, a dropped connection or a
// server error.
type CardModelCallFailedError struct {
	CardModelCallFailureMessage string
}

func (failure *CardModelCallFailedError) Error() string { return failure.CardModelCallFailureMessage }

// CardModelRefusedAccessError stops a run: a missing or rejected key, or a
// used quota, would fail every photo for a reason that is not about the photo.
type CardModelRefusedAccessError struct {
	CardModelRefusalMessage string
}

func (refusal *CardModelRefusedAccessError) Error() string { return refusal.CardModelRefusalMessage }

// One card can take a thinking model several minutes.
const cardModelCallTimeout = 6 * time.Minute
