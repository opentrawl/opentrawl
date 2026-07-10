package cli

import (
	"os"
	"time"

	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func appStatusMessage(source Source, status StatusEnvelope, now time.Time) *appv1.SourceStatus {
	counts := make([]*appv1.Count, 0, len(status.Counts))
	for _, count := range status.Counts {
		counts = append(counts, &appv1.Count{Id: count.ID, Display: formatCount(count)})
	}
	return &appv1.SourceStatus{
		AppId: status.AppID, Surface: status.Surface, State: status.State,
		Summary: status.Summary, Counts: counts,
		LastSyncedDisplay: freshnessText(status, now), ArchiveBytes: appArchiveBytes(source),
	}
}

func appArchiveBytes(source Source) int64 {
	paths, err := resolveSourcePaths(source)
	if err != nil {
		return 0
	}
	info, err := os.Stat(paths.paths.Archive)
	if err != nil {
		return 0
	}
	return info.Size()
}

func appSearchMessage(row SearchRow) *appv1.SearchHit {
	return &appv1.SearchHit{
		OpenRef: row.Ref, AppId: row.Source, Title: appSearchTitle(row),
		Snippet: row.Snippet, WhenDisplay: appSearchDate(row),
	}
}

func appSearchTitle(row SearchRow) string {
	if title := render.HumanIdentity(normalizeSelf(row.Where)); title != "" {
		return title
	}
	return render.HumanIdentity(normalizeSelf(row.Who))
}

func appSearchDate(row SearchRow) string {
	if !row.timeOK {
		return ""
	}
	if row.AllDay {
		return row.parsedTime.Format("2006-01-02")
	}
	return render.ShortLocalTime(row.parsedTime)
}
