package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	"google.golang.org/protobuf/proto"
)

const (
	appProcessHelperEnv = "TRAWL_APP_PROCESS_HELPER"
	appProcessModeEnv   = "TRAWL_APP_PROCESS_MODE"
)

func init() {
	if os.Getenv(appProcessHelperEnv) != "1" || len(os.Args) < 2 || os.Args[1] == trawlkit.HiddenWireSubcommand {
		return
	}
	crawlers, err := loadFakeCrawlers(os.Getenv(fakeCrawlersEnv))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	crawlerFactories = appProcessFactories(crawlers, os.Getenv(appProcessModeEnv))
	err = Execute(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil && ShouldPrintError(err) {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(ExitCode(err))
}

func runAppResponse(t *testing.T, message proto.Message, args ...string) {
	t.Helper()
	stdout, stderr, code := runCLI(t, append([]string{"__app"}, args...)...)
	logAppBoundary(t, args, stdout, stderr, code)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	readAppFrame(t, stdout, message)
}

func runAppResponseTimeout(t *testing.T, timeout time.Duration, message proto.Message, args ...string) {
	t.Helper()
	stdout, stderr, code := runCLITimeout(t, timeout, append([]string{"__app"}, args...)...)
	logAppBoundary(t, args, stdout, stderr, code)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	readAppFrame(t, stdout, message)
}

func logAppBoundary(t *testing.T, args []string, stdout, stderr string, code int) {
	t.Helper()
	t.Logf("in-process helper argv=%q stdin=%q stdout=%x stderr=%q exit=%d", append([]string{"__app"}, args...), "", []byte(stdout), stderr, code)
}

func runAppProcessResponse(t *testing.T, mode string, message proto.Message, args ...string) {
	t.Helper()
	ensureSyntheticHome(t)
	ensureFakeArchives(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, append([]string{"__app"}, args...)...) // #nosec G204 -- test helper executable and arguments are controlled here.
	cmd.Env = append(os.Environ(), appProcessHelperEnv+"=1", appProcessModeEnv+"="+mode)
	cmd.Stdin = bytes.NewReader(nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			code = exitError.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	t.Logf("process boundary executable=%q argv=%q stdin=%x stdout=%x stderr=%x exit=%d", executable, cmd.Args, []byte{}, stdout.Bytes(), stderr.Bytes(), code)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	readAppFrame(t, stdout.String(), message)
}

type appProcessErrorSource struct {
	*fakeSearchOpenSync
	mode string
}

func appProcessFactories(crawlers []fakeCrawler, mode string) []func() trawlkit.Crawler {
	if mode == "" {
		return fakeCrawlerFactories(crawlers)
	}
	factories := make([]func() trawlkit.Crawler, 0, len(crawlers))
	for _, crawler := range crawlers {
		crawler := crawler
		factories = append(factories, func() trawlkit.Crawler {
			return &appProcessErrorSource{fakeSearchOpenSync: &fakeSearchOpenSync{fakeSource: &fakeSource{crawler: crawler, manifest: fakeManifest(crawler)}}, mode: mode}
		})
	}
	return factories
}

func (s *appProcessErrorSource) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	if s.mode == "status-timeout" {
		return nil, fakeErrorBody("deadline_exceeded")
	}
	return s.fakeSearchOpenSync.Status(ctx, req)
}

func (s *appProcessErrorSource) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	if s.mode == "search-timeout" {
		return trawlkit.SearchResult{}, fakeErrorBody("deadline_exceeded")
	}
	return s.fakeSearchOpenSync.Search(ctx, req, query)
}

func (s *appProcessErrorSource) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	if s.mode == "open-timeout" {
		return fakeErrorBody("deadline_exceeded")
	}
	return s.fakeSearchOpenSync.Open(ctx, req, ref)
}

func (s *appProcessErrorSource) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	switch s.mode {
	case "sync-timeout":
		return nil, fakeErrorBody("deadline_exceeded")
	case "sync-partial":
		if s.Info().ID != "imessage" {
			return s.fakeSearchOpenSync.Sync(ctx, req)
		}
		return &trawlkit.SyncReport{Warnings: []string{"synthetic partial sync"}}, nil
	default:
		return s.fakeSearchOpenSync.Sync(ctx, req)
	}
}

func readAppFrame(t *testing.T, data string, message proto.Message) {
	t.Helper()
	raw := []byte(data)
	if len(raw) < 5 {
		t.Fatalf("frame length = %d, want 4-byte header and non-empty payload", len(raw))
	}
	size := binary.LittleEndian.Uint32(raw[:4])
	if int(size) != len(raw)-4 {
		t.Fatalf("frame payload size = %d, want %d", size, len(raw)-4)
	}
	if err := proto.Unmarshal(raw[4:], message); err != nil {
		t.Fatal(err)
	}
}

func TestAppStatusReturnsOneTypedResult(t *testing.T) {
	writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"iMessage"}`,
		status:   statusJSON("imessage", "ok"),
	})
	response := new(appv1.StatusResponse)
	runAppResponse(t, response, "status")
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetSources()) != 1 || len(response.GetFailures()) != 0 {
		t.Fatalf("response = %+v", response)
	}
	status := response.GetSources()[0]
	if status.GetAppId() != "imessage" || status.GetSurface() != "iMessage" || status.GetArchiveBytes() <= 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestAppStatusPreservesRowsAndNamesFailures(t *testing.T) {
	writeFakeCrawlers(t,
		fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), status: statusJSON("imessage", "ok")},
		fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), status: `not-json`},
	)
	response := new(appv1.StatusResponse)
	runAppResponse(t, response, "status")
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetSources()) != 1 || len(response.GetFailures()) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.GetFailures()[0].GetAppId() != "calendar" {
		t.Fatalf("failure = %+v", response.GetFailures()[0])
	}
}

func TestAppStatusTotalFailureAndTimeout(t *testing.T) {
	writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), status: `not-json`})
	response := new(appv1.StatusResponse)
	runAppResponse(t, response, "status")
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetSources()) != 0 || len(response.GetFailures()) != 1 {
		t.Fatalf("response = %+v", response)
	}

	timeout := appStatusResponse([]appStatusResult{{Source: Source{ID: "imessage", DisplayName: "iMessage"}, Err: sourceTimeout("status")}}, time.Now())
	if timeout.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || timeout.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("timeout response = %+v", timeout)
	}
}

func TestAppHelperProcessBoundary(t *testing.T) {
	t.Run("status complete", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), status: statusJSON("imessage", "ok")})
		response := new(appv1.StatusResponse)
		runAppProcessResponse(t, "", response, "status")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetSources()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("status partial", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), status: statusJSON("imessage", "ok")},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), status: `not-json`},
		)
		response := new(appv1.StatusResponse)
		runAppProcessResponse(t, "", response, "status")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetSources()) != 1 || len(response.GetFailures()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("status failed", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), status: `not-json`})
		response := new(appv1.StatusResponse)
		runAppProcessResponse(t, "", response, "status")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetSources()) != 0 || len(response.GetFailures()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("status timeout", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.StatusResponse)
		runAppProcessResponse(t, "status-timeout", response, "status")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("search empty", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 0)})
		response := new(appv1.SearchResponse)
		runAppProcessResponse(t, "", response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetHits()) != 0 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("search partial", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 1)},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), search: `not-json`, searchExit: 1},
		)
		response := new(appv1.SearchResponse)
		runAppProcessResponse(t, "", response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetHits()) != 1 || len(response.GetFailures()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("search partial with no rows", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 0)},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), search: `not-json`, searchExit: 1},
		)
		response := new(appv1.SearchResponse)
		runAppProcessResponse(t, "", response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetHits()) != 0 || len(response.GetFailures()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("search timeout", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.SearchResponse)
		runAppProcessResponse(t, "search-timeout", response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("search unknown source", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.SearchResponse)
		runAppProcessResponse(t, "", response, "search", "--source", "missing", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_NOT_FOUND {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("sync complete", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), sync: `{"state":"ok"}`})
		response := new(appv1.SyncResponse)
		runAppProcessResponse(t, "", response, "sync")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetSources()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("sync partial", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar")},
		)
		response := new(appv1.SyncResponse)
		runAppProcessResponse(t, "sync-partial", response, "sync")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetSources()) != 2 || response.GetSources()[0].GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("sync failed", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), sync: `not-json`},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), sync: `not-json`},
		)
		response := new(appv1.SyncResponse)
		runAppProcessResponse(t, "", response, "sync")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetSources()) != 2 || len(response.GetFailures()) != 2 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("sync timeout", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.SyncResponse)
		runAppProcessResponse(t, "sync-timeout", response, "sync")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("open complete", func(t *testing.T) {
		const ref = "imessage:msg/example"
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), openRef: ref, openHuman: "Synthetic open result"})
		response := new(appv1.OpenResponse)
		runAppProcessResponse(t, "", response, "open", ref)
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || string(response.GetOutput()) != "Synthetic open result\n" {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("open invalid ref", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.OpenResponse)
		runAppProcessResponse(t, "", response, "open", "imessage:")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_INVALID_INPUT {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("open unknown source", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage")})
		response := new(appv1.OpenResponse)
		runAppProcessResponse(t, "", response, "open", "missing:msg/example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_NOT_FOUND {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("open crawler failure", func(t *testing.T) {
		const ref = "imessage:msg/example"
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), openRef: ref, openHumanExit: 1})
		response := new(appv1.OpenResponse)
		runAppProcessResponse(t, "", response, "open", ref)
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_INTERNAL {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("open timeout", func(t *testing.T) {
		const ref = "imessage:msg/example"
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), openRef: ref})
		response := new(appv1.OpenResponse)
		runAppProcessResponse(t, "open-timeout", response, "open", ref)
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
			t.Fatalf("response = %+v", response)
		}
	})
}

func TestAppSearchResponses(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 0)})
		response := new(appv1.SearchResponse)
		runAppResponse(t, response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || response.GetResultLimit() != appSearchLimit || len(response.GetHits()) != 0 || len(response.GetFailures()) != 0 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("partial", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 1)},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), search: `not-json`, searchExit: 1},
		)
		response := new(appv1.SearchResponse)
		runAppResponse(t, response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetHits()) != 1 || len(response.GetFailures()) != 1 || response.GetFailures()[0].GetAppId() != "calendar" {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("partial with no rows", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 0)},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), search: `not-json`, searchExit: 1},
		)
		response := new(appv1.SearchResponse)
		runAppResponse(t, response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetHits()) != 0 || len(response.GetFailures()) != 1 {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("failed timeout", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 0), searchSleep: "20ms"})
		response := new(appv1.SearchResponse)
		runAppResponseTimeout(t, time.Millisecond, response, "search", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetHits()) != 0 || len(response.GetFailures()) != 1 || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("unknown source", func(t *testing.T) {
		writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 1)})
		response := new(appv1.SearchResponse)
		runAppResponse(t, response, "search", "--source", "missing", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetFailures()) != 1 || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_NOT_FOUND {
			t.Fatalf("response = %+v", response)
		}
	})
	t.Run("one source", func(t *testing.T) {
		writeFakeCrawlers(t,
			fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), search: searchResultsJSON("example", 1)},
			fakeCrawler{name: "calcrawl", metadata: metadataJSON("calendar"), search: searchResultsJSON("example", 1)},
		)
		response := new(appv1.SearchResponse)
		runAppResponse(t, response, "search", "--source", "calendar", "example")
		if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetHits()) != 1 || response.GetHits()[0].GetAppId() != "calendar" {
			t.Fatalf("response = %+v", response)
		}
	})
}

func TestAppSyncReportsEveryAttemptedSource(t *testing.T) {
	writeFakeCrawlers(t,
		fakeCrawler{name: "sourcea", metadata: metadataJSON("sourcea"), sync: `{"state":"ok"}`},
		fakeCrawler{name: "sourceb", metadata: metadataJSON("sourceb"), sync: `not-json`},
	)
	response := new(appv1.SyncResponse)
	runAppResponse(t, response, "sync")
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetSources()) != 2 || len(response.GetFailures()) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.GetSources()[1].GetAppId() != "sourceb" || response.GetSources()[1].GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED {
		t.Fatalf("sync source = %+v", response.GetSources()[1])
	}
}

func TestAppSyncOutcomeAndFailureClassification(t *testing.T) {
	source := Source{ID: "imessage", DisplayName: "iMessage"}
	complete := appSyncResponse([]Source{source}, []SyncResult{{Source: source.ID, State: "ok"}})
	if complete.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || complete.GetSources()[0].GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE {
		t.Fatalf("complete response = %+v", complete)
	}

	failed := appSyncResponse([]Source{source}, []SyncResult{{Source: source.ID, State: "error", Error: &ErrorBody{Code: "command_failed", Message: "synthetic command failure"}}})
	if failed.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || failed.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("failed response = %+v", failed)
	}

	partial := appSyncResponse([]Source{source}, []SyncResult{{Source: source.ID, State: "partial", Error: &ErrorBody{Code: "deadline_exceeded", Message: "synthetic deadline"}}})
	if partial.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || partial.GetSources()[0].GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || partial.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("partial response = %+v", partial)
	}

	allFailed := appSyncResponse([]Source{source, {ID: "calendar", DisplayName: "Calendar"}}, []SyncResult{
		{Source: source.ID, State: "error", Error: &ErrorBody{Code: "internal", Message: "synthetic failure"}},
		{Source: "calendar", State: "error", Error: &ErrorBody{Code: "sync_failed", Message: "synthetic failure"}},
	})
	if allFailed.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(allFailed.GetFailures()) != 2 {
		t.Fatalf("all failed response = %+v", allFailed)
	}
}

func TestAppOpenPreservesHumanBytesAndReturnsTypedFailures(t *testing.T) {
	const ref = "imessage:msg/example"
	writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), openRef: ref, openHuman: "Synthetic open result"})
	humanOutput, humanStderr, humanCode := runCLI(t, "open", ref)
	if humanCode != 0 || humanStderr != "" {
		t.Fatalf("human open stderr=%q exit=%d", humanStderr, humanCode)
	}
	response := new(appv1.OpenResponse)
	runAppResponse(t, response, "open", ref)
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || !bytes.Equal(response.GetOutput(), []byte(humanOutput)) {
		t.Fatalf("response = %+v, human output = %q", response, humanOutput)
	}

	unknownSource := new(appv1.OpenResponse)
	runAppResponse(t, unknownSource, "open", "missing:msg/example")
	if unknownSource.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || unknownSource.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_NOT_FOUND {
		t.Fatalf("unknown source response = %+v", unknownSource)
	}

	invalidRef := new(appv1.OpenResponse)
	runAppResponse(t, invalidRef, "open", "imessage:")
	if invalidRef.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || invalidRef.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_INVALID_INPUT {
		t.Fatalf("invalid ref response = %+v", invalidRef)
	}

	writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: metadataJSON("imessage"), openRef: ref, openHumanExit: 1})
	failedOpen := new(appv1.OpenResponse)
	runAppResponse(t, failedOpen, "open", ref)
	if failedOpen.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || failedOpen.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("failed open response = %+v", failedOpen)
	}

	timeout := appOpenResponse(Source{ID: "imessage", DisplayName: "iMessage"}, ref, nil, sourceTimeout("open"))
	if timeout.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || timeout.GetFailure().GetCode() != appv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("timeout response = %+v", timeout)
	}
}

func TestAppFrameRejectsOversizedResponseBeforeWriting(t *testing.T) {
	response := &appv1.OpenResponse{Output: bytes.Repeat([]byte("x"), appFrameLimit)}
	var stdout bytes.Buffer
	err := writeAppResponse(&stdout, response)
	if err == nil || stdout.Len() != 0 {
		t.Fatalf("write error = %v, stdout length = %d", err, stdout.Len())
	}
	if proto.Size(response) <= appFrameLimit {
		t.Fatalf("protobuf size = %d, want more than %d", proto.Size(response), appFrameLimit)
	}
}

func TestAppFailureCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		code string
		want appv1.FailureCode
	}{
		{name: "deadline", code: "deadline_exceeded", want: appv1.FailureCode_FAILURE_CODE_TIMEOUT},
		{name: "internal", code: "internal", want: appv1.FailureCode_FAILURE_CODE_INTERNAL},
		{name: "command", code: "command_failed", want: appv1.FailureCode_FAILURE_CODE_INTERNAL},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fakeError{body: ckoutput.ErrorBody{Code: test.code}}
			if got := appFailureCode(err); got != test.want {
				t.Fatalf("appFailureCode(%q) = %v, want %v", test.code, got, test.want)
			}
		})
	}
}
