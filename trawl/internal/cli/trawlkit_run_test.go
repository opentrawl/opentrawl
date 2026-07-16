package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func TestRunTrawlkitCapturedRunsRealCrawler(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)

	out, err := runTrawlkitCaptured([]string{"status", "--json"}, []trawlkit.Crawler{capturedRunCrawler{}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", out.Code, out.Stdout, out.Stderr)
	}
	stdout := string(out.Stdout)
	if !strings.Contains(stdout, `"app_id": "capturecrawl"`) || !strings.Contains(stdout, home) {
		t.Fatalf("stdout did not capture status JSON with synthetic HOME:\n%s", stdout)
	}
	if !strings.Contains(string(out.Stderr), "captured stderr") {
		t.Fatalf("stderr was not captured: %q", string(out.Stderr))
	}
}

type capturedRunCrawler struct{}

func (capturedRunCrawler) Info() trawlkit.Info {
	return trawlkit.Info{ID: "capturecrawl", DisplayName: "Capture"}
}

func (capturedRunCrawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	_ = ctx
	_ = req
	_, _ = fmt.Fprintln(os.Stderr, "captured stderr")
	status := control.NewStatus("capturecrawl", "home="+os.Getenv("HOME"))
	status.State = "ok"
	return &status, nil
}

func (capturedRunCrawler) Verbs() []trawlkit.Verb {
	return nil
}
