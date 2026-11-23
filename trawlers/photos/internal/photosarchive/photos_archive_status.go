package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CurrentAppleLibraryPhotoMonthlyCount struct {
	CalendarYear  uint32
	CalendarMonth uint32
	RecordCount   uint64
}

type CurrentAppleLibraryPhotoMonthlyHistory struct {
	OldestCaptureTimeUTC time.Time
	MonthlyCounts        []CurrentAppleLibraryPhotoMonthlyCount
	UndatedPhotoCount    uint64
}

func (photosArchive *PhotosArchive) CountCurrentAppleLibraryPhotos(ctx context.Context) (uint64, error) {
	var currentAppleLibraryPhotoCount uint64
	if err := photosArchive.database.QueryRowContext(ctx, `
select count(*) from apple_library_photo where absent_from_apple_library_since_time_utc is null`,
	).Scan(&currentAppleLibraryPhotoCount); err != nil {
		return 0, fmt.Errorf("count current Apple library photos: %w", err)
	}
	return currentAppleLibraryPhotoCount, nil
}

func (photosArchive *PhotosArchive) NewestCurrentAppleLibraryPhotoCaptureTime(ctx context.Context) (time.Time, error) {
	var newestCaptureTimeUTC string
	err := photosArchive.database.QueryRowContext(ctx, `
select capture_time_utc from apple_library_photo where absent_from_apple_library_since_time_utc is null order by capture_time_utc desc limit 1`,
	).Scan(&newestCaptureTimeUTC)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read newest Apple library photo capture time: %w", err)
	}
	return parseArchiveTime(newestCaptureTimeUTC)
}

func (photosArchive *PhotosArchive) ReadCurrentAppleLibraryPhotoMonthlyHistory(
	ctx context.Context,
) (CurrentAppleLibraryPhotoMonthlyHistory, error) {
	var history CurrentAppleLibraryPhotoMonthlyHistory

	var oldestCaptureTimeUTC sql.NullString
	if err := photosArchive.database.QueryRowContext(ctx, `
select min(capture_time_utc)
from apple_library_photo
where absent_from_apple_library_since_time_utc is null
  and capture_time_utc <> ''`,
	).Scan(&oldestCaptureTimeUTC); err != nil {
		return history, fmt.Errorf("read oldest Apple library photo capture time: %w", err)
	}
	if oldestCaptureTimeUTC.Valid && strings.TrimSpace(oldestCaptureTimeUTC.String) != "" {
		if parsed, err := parseArchiveTime(oldestCaptureTimeUTC.String); err == nil {
			history.OldestCaptureTimeUTC = parsed
		}
	}

	rows, err := photosArchive.database.QueryContext(ctx, `
select substr(capture_time_utc, 1, 7) as year_month, count(*)
from apple_library_photo
where absent_from_apple_library_since_time_utc is null
group by year_month`)
	if err != nil {
		return history, fmt.Errorf("read Apple library photo monthly counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var yearMonth string
		var count uint64
		if scanErr := rows.Scan(&yearMonth, &count); scanErr != nil {
			return history, fmt.Errorf("scan Apple library photo monthly count: %w", scanErr)
		}
		if count == 0 {
			continue
		}
		year, month, ok := parseCalendarYearMonthPrefix(yearMonth)
		if !ok {
			history.UndatedPhotoCount += count
			continue
		}
		history.MonthlyCounts = append(history.MonthlyCounts, CurrentAppleLibraryPhotoMonthlyCount{
			CalendarYear:  year,
			CalendarMonth: month,
			RecordCount:   count,
		})
	}
	if err := rows.Err(); err != nil {
		return history, err
	}
	sort.Slice(history.MonthlyCounts, func(i, j int) bool {
		if history.MonthlyCounts[i].CalendarYear != history.MonthlyCounts[j].CalendarYear {
			return history.MonthlyCounts[i].CalendarYear < history.MonthlyCounts[j].CalendarYear
		}
		return history.MonthlyCounts[i].CalendarMonth < history.MonthlyCounts[j].CalendarMonth
	})
	return history, nil
}

func parseCalendarYearMonthPrefix(yearMonth string) (uint32, uint32, bool) {
	if len(yearMonth) != 7 || yearMonth[4] != '-' {
		return 0, 0, false
	}
	year, yearErr := strconv.Atoi(yearMonth[:4])
	month, monthErr := strconv.Atoi(yearMonth[5:7])
	if yearErr != nil || monthErr != nil || year <= 0 || month < 1 || month > 12 {
		return 0, 0, false
	}
	return uint32(year), uint32(month), true
}
