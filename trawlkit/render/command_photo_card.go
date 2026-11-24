package render

import (
	"io"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func nextCommandWritingPhotoCard(link string, extraArguments ...string) NextCommand {
	return nextCommandWithLabel(nextCommandLabelWritePhotoCard, append([]string{"photos", "run", "write_photo_card", "--photo", link}, extraArguments...)...)
}

// writePhotoCardModelInputInspection prints the card model input exactly, line
// for line and unwrapped, because it is what the model reads.
func writePhotoCardModelInputInspection(writer io.Writer, response *photo.PhotoInspectionResponse, link string) error {
	record := response.GetPhotoRecord()
	nextCommands := []NextCommand{nextCommandWithLabel(nextCommandLabelMoreDetail, "photos", "inspect", link, "card"), nextCommandOpeningRecord(link)}
	if record.GetPictureFetchResult().GetPictureFetchOutcome() != photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
		warning := "Card model input: the picture has not been fetched, so no card can be written yet."
		if record.GetAppleLibraryPhotoFacts().GetCameraLocation() != nil {
			warning = "Card model input: the picture has not been fetched, so the camera's direction is missing below and no card can be written yet."
		}
		if err := writeLines(writer, append(Wrap(warning, OutputWidth(writer)), "")); err != nil {
			return err
		}
		nextCommands = append([]NextCommand{nextCommandWithLabel(nextCommandLabelFetchPicture, "photos", "run", "fetch_picture", "--photo", link)}, nextCommands...)
	}
	text := strings.TrimRight(response.GetCurrentPhotoCardModelInput().GetPhotoCardModelInputText(), "\n")
	if err := writeLines(writer, strings.Split(text, "\n")); err != nil {
		return err
	}
	if err := writeLine(writer, ""); err != nil {
		return err
	}
	return writeNextCommands(writer, nextCommands)
}

func writePhotoCardInspection(writer io.Writer, record *photo.OpenedPhotoRecord, link string) error {
	photoCard := record.GetPhotoCard()
	modelInputCommand := nextCommandWithLabel(nextCommandLabelPhotoCardModelInput, "photos", "inspect", link, "card-model-input")
	if photoCard == nil {
		if err := writeLine(writer, "Card: no card has been written for this photo."); err != nil {
			return err
		}
		return writeNextCommands(writer, []NextCommand{nextCommandWritingPhotoCard(link), modelInputCommand, nextCommandOpeningRecord(link)})
	}
	fields := []cardField{{label: "Outcome", value: photoCardOutcomeForHumanOutput(photoCard.GetPhotoCardOutcome())}}
	var nextCommands []NextCommand
	switch photoCard.GetPhotoCardOutcome() {
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN:
		fields = append(fields,
			cardField{label: "Summary", value: photoCard.GetOneLineSummary()},
			cardField{label: "Place shown", value: photoCard.GetPlaceShownName()},
			cardField{label: "Place reasoning", value: photoCard.GetPlaceShownReasoning()},
			cardField{label: "Description", value: photoCard.GetDescription()},
		)
		fields = append(fields, photoCardVisibleTextFields(photoCard)...)
		if len(photoCard.GetVisibleTextGroups()) == 0 {
			fields = append(fields, cardField{label: "Visible text", value: "None"})
		}
		changedSince := "No"
		if record.GetCardModelInputChangedSinceCardWasWritten() {
			changedSince = "Yes; the card is remade only with --recard"
			nextCommands = append(nextCommands, nextCommandWritingPhotoCard(link, "--recard"))
		}
		fields = append(fields, cardField{label: "Facts changed since", value: changedSince})
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_ANSWER_INVALID, photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED:
		fields = append(fields, cardField{label: "Failure", value: photoCard.GetPhotoCardFailureMessage()})
		retry := "Only with --retry-failed"
		if photoCard.GetRetryNotBeforeTimeUtc() != nil {
			retry = "After " + formatExactUTCTimeForInspection(photoCard.GetRetryNotBeforeTimeUtc())
			nextCommands = append(nextCommands, nextCommandWritingPhotoCard(link))
		} else {
			nextCommands = append(nextCommands, nextCommandWritingPhotoCard(link, "--retry-failed"))
		}
		fields = append(fields, cardField{label: "Tried again", value: retry})
		if answer := strings.TrimSpace(photoCard.GetRawModelAnswer()); answer != "" {
			fields = append(fields, cardField{label: "Model's answer", value: answer})
		}
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_UNSPECIFIED:
	}
	cardModelDescription := photoCard.GetCardModelName()
	if effort := strings.TrimSpace(photoCard.GetCardModelEffort()); effort != "" {
		cardModelDescription += " (" + effort + ")"
	}
	cardModelDescription += " · " + cardModelProviderForHumanOutput(photoCard.GetCardModelProvider())
	if photoCard.PictureLongEdgePixelLimitUsed != nil {
		cardModelDescription += " · " + FormatInteger(int64(photoCard.GetPictureLongEdgePixelLimitUsed())) + " px"
	}
	fields = append(fields,
		cardField{label: "Card model", value: cardModelDescription},
		cardField{label: "Tokens", value: photoCardTokensForHumanOutput(photoCard)},
		cardField{label: "Written", value: formatExactUTCTimeForInspection(photoCard.GetCardWrittenTimeUtc())},
	)
	if err := writeCard(writer, card{title: "Card", fields: fields}); err != nil {
		return err
	}
	return writeNextCommands(writer, append(nextCommands, modelInputCommand, nextCommandOpeningRecord(link)))
}

func photoCardOutcomeForHumanOutput(outcome photo.PhotoCardOutcome) string {
	switch outcome {
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN:
		return "Written"
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_ANSWER_INVALID:
		return "The model's answer did not follow the asked format"
	case photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED:
		return "The model call failed"
	default:
		return "Not known"
	}
}

func cardModelProviderForHumanOutput(provider photo.CardModelProvider) string {
	switch provider {
	case photo.CardModelProvider_CARD_MODEL_PROVIDER_OLLAMA_CLOUD:
		return "Ollama Cloud"
	case photo.CardModelProvider_CARD_MODEL_PROVIDER_CODEX_COMMAND_LINE:
		return "Codex command line"
	case photo.CardModelProvider_CARD_MODEL_PROVIDER_ANTIGRAVITY_ACP:
		return "Antigravity ACP"
	case photo.CardModelProvider_CARD_MODEL_PROVIDER_ANTHROPIC_API:
		return "Anthropic API"
	case photo.CardModelProvider_CARD_MODEL_PROVIDER_CLAUDE_COMMAND_LINE:
		return "Claude command line"
	default:
		return "provider not known"
	}
}

func photoCardTokensForHumanOutput(photoCard *photo.PhotoCard) string {
	var parts []string
	for _, count := range []struct {
		label string
		value *int64
	}{{"in", photoCard.InputTokens}, {"out", photoCard.OutputTokens}, {"of them reasoning", photoCard.ReasoningTokens}} {
		if count.value != nil {
			parts = append(parts, FormatInteger(*count.value)+" "+count.label)
		}
	}
	if len(parts) == 0 {
		return "Not reported by the provider"
	}
	return strings.Join(parts, " · ")
}
