package archive

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/model"
)

type classifyLogger struct {
	sink ClassifyLogSink
}

func (logger classifyLogger) logOutcome(write classifyWrite) {
	switch write.outcome {
	case contentOutcomeFailedDownload:
		fields := []string{
			logTokenField("asset_ref", AssetRef(write.input.AssetID)),
			logStringField("reason", publicClassifyErrorReason(write.downloadErr, "original export failed")),
		}
		if errors.Is(write.downloadErr, photos.ErrExportAlreadyRunning) {
			fields = append(fields, "lock_conflict=true")
		}
		logger.warn("failed_download", fields...)
	case contentOutcomeNotInPhotoKit:
		logger.warn("not_in_photokit",
			logTokenField("asset_ref", AssetRef(write.input.AssetID)),
			logStringField("reason", "photokit asset not found"),
		)
	case contentOutcomeFailedParse:
		logger.warn("failed_parse",
			logTokenField("asset_ref", AssetRef(write.input.AssetID)),
			logStringField("reason", "model response could not be parsed"),
		)
	case contentOutcomeFailedModel:
		logger.warn("failed_model",
			logTokenField("asset_ref", AssetRef(write.input.AssetID)),
			logStringField("reason", publicClassifyErrorReason(write.contentErr, "model request failed")),
		)
	case contentOutcomeClassified:
		// Successes log too: stage durations per card are the structural
		// answer to "where does the time go" — silence hides bottlenecks.
		logger.info("card_written",
			logTokenField("asset_ref", AssetRef(write.input.AssetID)),
			logInt64Field("download_ms", write.downloadDuration.Milliseconds()),
			logInt64Field("model_ms", write.modelDuration.Milliseconds()),
			logIntField("model_attempts", write.modelAttempts),
			logInt64Field("write_ms", write.writeDuration.Milliseconds()),
		)
	}
}

func (logger classifyLogger) logPlaceGeocode(key, outcome string, duration time.Duration, reason string) {
	fields := []string{
		logTokenField("key", key),
		logTokenField("outcome", outcome),
		logInt64Field("duration_ms", duration.Milliseconds()),
		logStringField("reason", reason),
	}
	switch outcome {
	case "ok":
		logger.info("place_geocode", fields...)
	default:
		logger.warn("place_geocode", fields...)
	}
}

func (logger classifyLogger) logPlaceParked(input classifyInput, reason string) {
	logger.warn("place_parked",
		logTokenField("asset_ref", AssetRef(input.AssetID)),
		logStringField("reason", reason),
	)
}

func (logger classifyLogger) logPlaceUnparked(input classifyInput, reason string) {
	logger.info("place_unparked",
		logTokenField("asset_ref", AssetRef(input.AssetID)),
		logStringField("reason", reason),
	)
}

func (logger classifyLogger) info(event string, fields ...string) {
	if logger.sink == nil {
		return
	}
	_ = logger.sink.Info(event, strings.Join(nonEmptyLogFields(fields), " "))
}

func (logger classifyLogger) warn(event string, fields ...string) {
	if logger.sink == nil {
		return
	}
	_ = logger.sink.Warn(event, strings.Join(nonEmptyLogFields(fields), " "))
}

func logTokenField(key, value string) string {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return ""
	}
	return key + "=" + value
}

func logStringField(key, value string) string {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return ""
	}
	return key + "=" + strconv.Quote(value)
}

func logIntField(key string, value int) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return key + "=" + strconv.Itoa(value)
}

func logInt64Field(key string, value int64) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return key + "=" + strconv.FormatInt(value, 10)
}

func publicClassifyErrorReason(err error, fallback string) string {
	switch {
	case err == nil:
		return fallback
	case errors.Is(err, photos.ErrPhotoKitAssetNotFound):
		return "photokit asset not found"
	case errors.Is(err, photos.ErrExportAlreadyRunning):
		return "photokit export already running"
	case errors.Is(err, context.Canceled):
		return "context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	}
	var httpErr *model.HTTPError
	if errors.As(err, &httpErr) {
		return "model returned " + httpErr.Status
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "network timeout"
	}
	if isModelParseFailure(err) {
		return "model response could not be parsed"
	}
	return fallback
}

func nonEmptyLogFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			out = append(out, field)
		}
	}
	return out
}

func (logger classifyLogger) logPhase(phase string, duration time.Duration, fields ...string) {
	all := append([]string{logInt64Field("duration_ms", duration.Milliseconds())}, fields...)
	logger.info(phase, all...)
}
