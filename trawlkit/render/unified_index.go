package render

import (
	"fmt"
	"strings"
	"time"
)

// UnifiedIndexTrawlerCountPresentation is one trawler's row in index status or rebuild output.
type UnifiedIndexTrawlerCountPresentation struct {
	TrawlerIdentity    string
	TrawlerDisplayName string
	RecordCount        int64
	IndexCursor        string
	IndexedAtUTC       time.Time
}

// UnifiedIndexStatusPresentation carries the typed state of index.db.
type UnifiedIndexStatusPresentation struct {
	IndexPath          string
	Exists             bool
	UpdatedAtUTC       time.Time
	TotalIndexedCount  int64
	AnchorCount        int64
	MoneyMovementCount int64
	TimeWindowCount    int64
	DatabaseByteCount  uint64
	Trawlers           []UnifiedIndexTrawlerCountPresentation
}

// UnifiedIndexRebuildPresentation carries the typed result of `trawl index rebuild`.
type UnifiedIndexRebuildPresentation struct {
	IndexPath          string
	TotalIndexedCount  int64
	AnchorCount        int64
	MoneyMovementCount int64
	TimeWindowCount    int64
	DatabaseByteCount  uint64
	TotalElapsed       time.Duration
	Trawlers           []UnifiedIndexTrawlerCountPresentation
}

// AroundMatchRowPresentation is one joined record row rendered by `trawl around`.
type AroundMatchRowPresentation struct {
	When    string
	How     string
	Trawler string
	Summary string
	Link    string
}

// AroundJoinPresentation carries the typed result of `trawl around`.
type AroundJoinPresentation struct {
	AnchorSummary string
	AnchorLink    string
	WindowRange   string
	Matches       []AroundMatchRowPresentation
	TotalFound    int
}

func (output TrawlCommandOutput) WriteUnifiedIndexStatus(status UnifiedIndexStatusPresentation) error {
	w := output.standardOutput
	if !status.Exists {
		if err := writeLine(w, "Unified index has not been built yet."); err != nil {
			return err
		}
		return writeNextCommands(w, []NextCommand{{
			nextCommandLabel:                     nextCommandLabelUpdateArchive,
			trawlCommandArgumentsAfterInvocation: []string{"index", "rebuild"},
		}})
	}
	header := fmt.Sprintf(
		"Unified index: %s records · %s anchors · %s money movements · %s",
		FormatInteger(status.TotalIndexedCount),
		FormatInteger(status.AnchorCount),
		FormatInteger(status.MoneyMovementCount),
		formatIndexByteCount(status.DatabaseByteCount),
	)
	if !status.UpdatedAtUTC.IsZero() {
		header += " · updated " + status.UpdatedAtUTC.Local().Format("2006-01-02 15:04 -07:00")
	}
	if err := writeLines(w, Wrap(header, OutputWidth(w))); err != nil {
		return err
	}
	if len(status.Trawlers) == 0 {
		return nil
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	cols := []TableColumn{
		{Header: "Trawler", MinimumWidth: 10, NeverTruncateCellValues: true},
		{Header: "Records", MinimumWidth: 8, AlignRight: true, NeverTruncateCellValues: true},
		{Header: "Indexed", MinimumWidth: 16, NeverTruncateCellValues: true},
	}
	rows := make([][]string, 0, len(status.Trawlers))
	for _, t := range status.Trawlers {
		name := strings.TrimSpace(t.TrawlerDisplayName)
		if name == "" {
			name = t.TrawlerIdentity
		}
		when := ""
		if !t.IndexedAtUTC.IsZero() {
			when = t.IndexedAtUTC.Local().Format("2006-01-02 15:04 -07:00")
		}
		rows = append(rows, []string{name, FormatInteger(t.RecordCount), when})
	}
	return writeHumanRecordRowsWithPrimaryContentColumn(w, cols, rows, 0)
}

func (output TrawlCommandOutput) WriteUnifiedIndexRebuildReport(report UnifiedIndexRebuildPresentation) error {
	w := output.standardOutput
	header := fmt.Sprintf(
		"Rebuilt unified index: %s records · %s anchors · %s money movements · %s in %s.",
		FormatInteger(report.TotalIndexedCount),
		FormatInteger(report.AnchorCount),
		FormatInteger(report.MoneyMovementCount),
		formatIndexByteCount(report.DatabaseByteCount),
		report.TotalElapsed.Round(time.Millisecond),
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
		{Header: "Records", MinimumWidth: 8, AlignRight: true, NeverTruncateCellValues: true},
	}
	rows := make([][]string, 0, len(report.Trawlers))
	for _, t := range report.Trawlers {
		name := strings.TrimSpace(t.TrawlerDisplayName)
		if name == "" {
			name = t.TrawlerIdentity
		}
		rows = append(rows, []string{name, FormatInteger(t.RecordCount)})
	}
	return writeHumanRecordRowsWithPrimaryContentColumn(w, cols, rows, 0)
}

func (output TrawlCommandOutput) WriteAroundResults(around AroundJoinPresentation) error {
	w := output.standardOutput
	heading := "Around " + strings.TrimSpace(around.AnchorSummary)
	if strings.TrimSpace(around.WindowRange) != "" {
		heading += " (" + strings.TrimSpace(around.WindowRange) + ")"
	}
	if err := writeLines(w, Wrap(heading, OutputWidth(w))); err != nil {
		return err
	}
	if len(around.Matches) == 0 {
		return writeLine(w, "No related archive records found in that window.")
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	cols := []TableColumn{
		{Header: "When", MinimumWidth: 10, NeverTruncateCellValues: true},
		{Header: "Source", MinimumWidth: 8, NeverTruncateCellValues: true},
		{Header: "Join", MinimumWidth: 14, Wrap: true, MaximumWrappedLines: 2},
		{Header: "What", MinimumWidth: 18, Wrap: true, MaximumWrappedLines: 3},
		{Header: "Link", MinimumWidth: 12, NeverTruncateCellValues: true},
	}
	rows := make([][]string, 0, len(around.Matches))
	for _, m := range around.Matches {
		rows = append(rows, []string{m.When, m.Trawler, m.How, m.Summary, m.Link})
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(w, cols, rows, 3); err != nil {
		return err
	}
	if len(around.Matches) > 0 && strings.TrimSpace(around.Matches[0].Link) != "" {
		return writeNextCommands(w, []NextCommand{{
			nextCommandLabel:                     nextCommandLabelOpenRecord,
			trawlCommandArgumentsAfterInvocation: []string{"open", around.Matches[0].Link},
		}})
	}
	return nil
}

func formatIndexByteCount(bytes uint64) string {
	const (
		kb = 1024.0
		mb = 1024.0 * 1024.0
		gb = 1024.0 * 1024.0 * 1024.0
	)
	f := float64(bytes)
	switch {
	case f >= gb:
		return fmt.Sprintf("%.2f GB", f/gb)
	case f >= mb:
		return fmt.Sprintf("%.1f MB", f/mb)
	case f >= kb:
		return fmt.Sprintf("%.1f KB", f/kb)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
