package render

import (
	"fmt"
	"strings"
	"time"
)

// SemanticIndexTrawlerCountPresentation is one trawler row in semantic status or build output.
type SemanticIndexTrawlerCountPresentation struct {
	TrawlerIdentity    string
	TrawlerDisplayName string
	ChunkCount         int64
}

// SemanticIndexStatusPresentation carries the typed state of disposable semantic.db.
type SemanticIndexStatusPresentation struct {
	DatabaseExists        bool
	SemanticSearchEnabled bool
	ModelIdentifier       string
	ModelWeightsCached    bool
	ModelWeightsByteCount uint64
	HelperBinaryFound     bool
	HelperRunning         bool
	HelperModelLoaded     bool
	HelperActiveMetalByte uint64
	ChunkerSchema         string
	EmbeddingDimensions   int
	TotalChunkCount       int64
	DatabaseByteCount     uint64
	UpdatedAtUTC          time.Time
	Trawlers              []SemanticIndexTrawlerCountPresentation
}

// SemanticIndexBuildPresentation carries the typed result of `trawl semantic build`.
type SemanticIndexBuildPresentation struct {
	ModelIdentifier     string
	TotalChunksInIndex  int64
	ChunksNewlyEmbedded int64
	ChunksUnchanged     int64
	ChunksRemoved       int64
	Elapsed             time.Duration
	DatabaseByteCount   uint64
	Trawlers            []SemanticIndexTrawlerCountPresentation
}

func (output TrawlCommandOutput) WriteSemanticIndexStatus(status SemanticIndexStatusPresentation) error {
	w := output.standardOutput
	stateWord := "disabled"
	if status.SemanticSearchEnabled {
		stateWord = "enabled"
	}
	header := fmt.Sprintf(
		"Semantic search: %s · %s chunks · %s",
		stateWord,
		FormatInteger(status.TotalChunkCount),
		formatIndexByteCount(status.DatabaseByteCount),
	)
	if !status.UpdatedAtUTC.IsZero() {
		header += " · updated " + status.UpdatedAtUTC.Local().Format("2006-01-02 15:04 -07:00")
	}
	if err := writeLines(w, Wrap(header, OutputWidth(w))); err != nil {
		return err
	}

	weightsDetail := "not downloaded"
	if status.ModelWeightsCached {
		weightsDetail = fmt.Sprintf("cached (%s)", formatIndexByteCount(status.ModelWeightsByteCount))
	}
	helperDetail := "not found"
	if status.HelperBinaryFound {
		helperDetail = "idle (0 B)"
		if status.HelperRunning {
			if status.HelperModelLoaded {
				helperDetail = fmt.Sprintf("loaded (%s GPU)", formatIndexByteCount(status.HelperActiveMetalByte))
			} else {
				helperDetail = "running (model unloaded)"
			}
		}
	}
	summary := fmt.Sprintf(
		"Model: %s (%dd int8, %s) · Weights: %s · Helper: %s",
		status.ModelIdentifier,
		status.EmbeddingDimensions,
		status.ChunkerSchema,
		weightsDetail,
		helperDetail,
	)
	if err := writeLines(w, Wrap(summary, OutputWidth(w))); err != nil {
		return err
	}

	if len(status.Trawlers) > 0 {
		if err := writeLine(w, ""); err != nil {
			return err
		}
		cols := []TableColumn{
			{Header: "Trawler", MinimumWidth: 10, NeverTruncateCellValues: true},
			{Header: "Chunks", MinimumWidth: 8, AlignRight: true, NeverTruncateCellValues: true},
		}
		rows := make([][]string, 0, len(status.Trawlers))
		for _, t := range status.Trawlers {
			name := strings.TrimSpace(t.TrawlerDisplayName)
			if name == "" {
				name = t.TrawlerIdentity
			}
			rows = append(rows, []string{name, FormatInteger(t.ChunkCount)})
		}
		if err := writeHumanRecordRowsWithPrimaryContentColumn(w, cols, rows, 0); err != nil {
			return err
		}
	}

	if !status.SemanticSearchEnabled {
		return writeNextCommands(w, []NextCommand{{
			nextCommandLabel:                     nextCommandLabelUpdateArchive,
			trawlCommandArgumentsAfterInvocation: []string{"semantic", "enable"},
		}})
	}
	if status.TotalChunkCount == 0 {
		return writeNextCommands(w, []NextCommand{{
			nextCommandLabel:                     nextCommandLabelUpdateArchive,
			trawlCommandArgumentsAfterInvocation: []string{"semantic", "build"},
		}})
	}
	return nil
}

func (output TrawlCommandOutput) WriteSemanticIndexBuildReport(report SemanticIndexBuildPresentation) error {
	w := output.standardOutput
	header := fmt.Sprintf(
		"Updated semantic index: %s chunks (%s embedded, %s unchanged, %s removed) · %s in %s.",
		FormatInteger(report.TotalChunksInIndex),
		FormatInteger(report.ChunksNewlyEmbedded),
		FormatInteger(report.ChunksUnchanged),
		FormatInteger(report.ChunksRemoved),
		formatIndexByteCount(report.DatabaseByteCount),
		report.Elapsed.Round(time.Millisecond),
	)
	if err := writeLines(w, Wrap(header, OutputWidth(w))); err != nil {
		return err
	}
	if len(report.Trawlers) == 0 {
		return nil
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	cols := []TableColumn{
		{Header: "Trawler", MinimumWidth: 10, NeverTruncateCellValues: true},
		{Header: "Chunks", MinimumWidth: 8, AlignRight: true, NeverTruncateCellValues: true},
	}
	rows := make([][]string, 0, len(report.Trawlers))
	for _, t := range report.Trawlers {
		name := strings.TrimSpace(t.TrawlerDisplayName)
		if name == "" {
			name = t.TrawlerIdentity
		}
		rows = append(rows, []string{name, FormatInteger(t.ChunkCount)})
	}
	return writeHumanRecordRowsWithPrimaryContentColumn(w, cols, rows, 0)
}

func (output TrawlCommandOutput) WriteSemanticSearchDisabled(deletedIndex bool) error {
	w := output.standardOutput
	msg := "Semantic search disabled. Search now uses the deterministic keyword index only."
	if deletedIndex {
		msg = "Semantic search disabled and disposable semantic.db removed. Search now uses the deterministic keyword index only."
	}
	return writeLines(w, Wrap(msg, OutputWidth(w)))
}
