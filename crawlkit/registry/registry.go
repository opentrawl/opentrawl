// Package registry is the one place that knows which crawlers exist.
//
// It holds the canonical list of crawler binaries and discovers the ones
// installed on PATH by probing each `metadata --json` into a
// control.Manifest. trawl is the single caller: it used to keep its own
// hardcoded binary list plus ~/.trawl/apps drop-in files and a private
// metadata struct — this package replaces all three with one discoverer.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
)

// probeTimeout bounds a single metadata probe. Discovery fans out across
// every registered binary, so a wedged child must not hang the parent.
const probeTimeout = 10 * time.Second

// Binaries is the canonical set of crawler binaries, in the order trawl
// lists them. Registration is this list — no plugins, no config files.
var Binaries = []string{
	"imsgcrawl",
	"telecrawl",
	"wacrawl",
	"clawdex",
	"photoscrawl",
	"gogcrawl",
	"calcrawl",
	"birdcrawl",
}

// Crawler is one discovered binary: its registered name, the resolved
// PATH, the manifest it reported, and any probe error. On error Manifest
// is zero and Err explains why, so the caller can surface an unhealthy
// crawler instead of silently dropping it.
type Crawler struct {
	Name     string
	Path     string
	Manifest control.Manifest
	Err      error
}

// Discover looks up each registered binary on PATH and probes its
// manifest. Binaries absent from PATH are skipped; the returned slice
// keeps Binaries order.
func Discover(ctx context.Context) []Crawler {
	crawlers := make([]Crawler, 0, len(Binaries))
	for _, name := range Binaries {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		crawler := Crawler{Name: name, Path: path}
		manifest, err := probe(ctx, path)
		if err != nil {
			crawler.Err = err
		} else {
			crawler.Manifest = manifest
		}
		crawlers = append(crawlers, crawler)
	}
	return crawlers
}

func probe(ctx context.Context, path string) (control.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "metadata", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return control.Manifest{}, err
	}
	var manifest control.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		return control.Manifest{}, err
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return control.Manifest{}, errors.New("metadata id is empty")
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	return manifest, nil
}
