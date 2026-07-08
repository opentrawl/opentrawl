package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
)

func TestRunCrawlkitCapturedRunsRealCrawler(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)

	out, err := runCrawlkitCaptured([]string{"status", "--json"}, []crawlkit.Crawler{capturedRunCrawler{}})
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

func (capturedRunCrawler) Info() crawlkit.Info {
	return crawlkit.Info{ID: "capturecrawl", DisplayName: "Capture"}
}

func (capturedRunCrawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	_ = ctx
	_ = req
	_, _ = fmt.Fprintln(os.Stderr, "captured stderr")
	status := control.NewStatus("capturecrawl", "home="+os.Getenv("HOME"))
	status.State = "ok"
	return &status, nil
}

func (capturedRunCrawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	_ = ctx
	_ = req
	return &crawlkit.Doctor{}, nil
}

func (capturedRunCrawler) Verbs() []crawlkit.Verb {
	return nil
}
