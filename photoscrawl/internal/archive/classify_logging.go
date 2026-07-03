package archive

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/openclaw/photoscrawl/internal/modelclient"
	"github.com/openclaw/photoscrawl/internal/photos"
)

type classifyLogger struct {
	sink ClassifyLogSink
}

func (logger classifyLogger) logOutcome(write classifyWrite) {
	switch write.outcome {
	case contentOutcomeFailedDownload:
		fields := []string{
			logTokenField("asset_ref", assetRef(write.input.AssetID)),
			logStringField("reason", publicClassifyErrorReason(write.downloadErr, "original export failed")),
		}
		if errors.Is(write.downloadErr, photos.ErrExportAlreadyRunning) {
			fields = append(fields, "lock_conflict=true")
		}
		logger.warn("failed_download", fields...)
	case contentOutcomeNotInPhotoKit:
		logger.warn("not_in_photokit",
			logTokenField("asset_ref", assetRef(write.input.AssetID)),
			logStringField("reason", "photokit asset not found"),
		)
	case contentOutcomeFailedParse:
		logger.warn("failed_parse",
			logTokenField("asset_ref", assetRef(write.input.AssetID)),
			logStringField("reason", "model response could not be parsed"),
		)
	case contentOutcomeFailedModel:
		logger.warn("failed_model",
			logTokenField("asset_ref", assetRef(write.input.AssetID)),
			logStringField("reason", publicClassifyErrorReason(write.contentErr, "model request failed")),
		)
	}
}

func (logger classifyLogger) logModelRetry(input classifyInput, attempt int, err error, retry retryDecision, limiterBefore, limiterAfter int) {
	retryKind := "retryable"
	if retry.rateLimited {
		retryKind = "rate_limited"
	} else if retry.transient {
		retryKind = "transient"
	}
	logger.warn("model_retry",
		logTokenField("asset_ref", assetRef(input.AssetID)),
		logIntField("attempt", attempt),
		logIntField("next_attempt", attempt+1),
		logStringField("reason", publicClassifyErrorReason(err, "model request failed")),
		logTokenField("retry_kind", retryKind),
		logIntField("limiter_before", limiterBefore),
		logIntField("limiter_after", limiterAfter),
	)
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
	var httpErr *modelclient.HTTPError
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
