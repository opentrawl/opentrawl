package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/photoscrawl/internal/archive"
)

func writeStatus(w io.Writer, format output.Format, status archive.StatusResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "status", status)
	}
	return printStatusText(w, status)
}

func printStatusText(w io.Writer, status archive.StatusResult) error {
	if _, err := fmt.Fprintf(w, "Status: %s\n%s\n", status.State, status.Summary); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\nCounts:\n"); err != nil {
		return err
	}
	if len(status.Counts) == 0 {
		if _, err := io.WriteString(w, "  none\n"); err != nil {
			return err
		}
	}
	for _, count := range status.Counts {
		if _, err := fmt.Fprintf(w, "  %s: %d\n", count.Label, count.Value); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\nPaths:\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Database: %s\n", emptyDash(status.DatabasePath)); err != nil {
		return err
	}
	if status.DatabaseBytes > 0 {
		if _, err := fmt.Fprintf(w, "  Size: %d bytes\n", status.DatabaseBytes); err != nil {
			return err
		}
	}
	if status.LastImportAt != "" {
		if _, err := fmt.Fprintf(w, "  Last import: %s\n", status.LastImportAt); err != nil {
			return err
		}
	}
	if status.Freshness != nil && status.Freshness.LastSync != "" {
		if _, err := fmt.Fprintf(w, "\nFreshness:\n  Last sync: %s\n", status.Freshness.LastSync); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctor(w io.Writer, format output.Format, result archive.DoctorResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "doctor", result)
	}
	return printDoctorText(w, result)
}

func printDoctorText(w io.Writer, result archive.DoctorResult) error {
	if _, err := io.WriteString(w, "Doctor checks:\n"); err != nil {
		return err
	}
	for _, check := range result.Checks {
		if _, err := fmt.Fprintf(w, "  %s: %s", check.ID, check.State); err != nil {
			return err
		}
		if check.Message != "" {
			if _, err := fmt.Fprintf(w, " - %s", check.Message); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		if check.Remedy != "" {
			if _, err := fmt.Fprintf(w, "    Remedy: %s\n", check.Remedy); err != nil {
				return err
			}
		}
	}
	return nil
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
