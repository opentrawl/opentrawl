package cli

import (
	"fmt"
	"strings"
)

type OpenCmd struct {
	Ref string `arg:"" help:"Source-prefixed ref"`
}

func (c *OpenCmd) Run(r *Runtime) error {
	sourceID, _, ok := splitOpenRef(c.Ref)
	if !ok {
		return r.writeError("invalid_ref",
			"Ref is missing a source or path.",
			"refs look like <source>:<path>, for example imsgcrawl:msg/8842")
	}
	source, err := r.selectedSource(sourceID)
	if err != nil {
		return err
	}
	data, err := runCrawlerJSONWithArgs(r.ctx, source.Path, "open", c.Ref)
	if err != nil {
		return r.openFailed(c.Ref, source.ID)
	}
	var payload any
	if err := decodeContractJSON(data, &payload); err != nil {
		return r.openFailed(c.Ref, source.ID)
	}
	if r.root.JSON {
		_, err := r.stdout.Write(data)
		return err
	}
	return renderOpenPayload(r.stdout, payload)
}

func splitOpenRef(ref string) (string, string, bool) {
	source, path, found := strings.Cut(ref, ":")
	if !found {
		return "", "", false
	}
	source = strings.TrimSpace(source)
	path = strings.TrimSpace(path)
	if source == "" || path == "" {
		return "", "", false
	}
	return source, path, true
}

func (r *Runtime) openFailed(ref, source string) error {
	return r.writeError("open_failed",
		fmt.Sprintf("Could not open ref %q.", ref),
		fmt.Sprintf("run: trawl doctor %s", source))
}
