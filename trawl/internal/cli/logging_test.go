package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoggingFailureDoesNotBlockACommand(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".opentrawl"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", writeFakeCrawlers(t, fakeCrawler{
		name:     "gogcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status"],"id":"gmail","display_name":"Gmail"}`,
		status:   `{"schema_version":"1","app_id":"gmail","state":"ok","summary":"Ready"}`,
	}))

	stdout, stderr, code := runCLI(t, "status")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout == "" || stderr != "" {
		t.Fatalf("stdout=%s stderr=%s", stdout, stderr)
	}
}
