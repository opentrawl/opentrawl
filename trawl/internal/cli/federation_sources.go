package cli

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

func (r *Runtime) federationStatusSources(sources []Source) []federation.StatusSource {
	out := make([]federation.StatusSource, 0, len(sources))
	for _, source := range sources {
		source := source
		manifest := cloneManifest(source.Manifest)
		if source.MetadataErr != nil {
			out = append(out, federation.StatusSource{Manifest: manifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
				return nil, federation.FailureForError(manifest, "status", source.MetadataErr)
			}})
			continue
		}
		if _, ok := manifest.Commands["status"]; !ok {
			out = append(out, federation.StatusSource{Manifest: manifest, SkipReason: "Status is not supported."})
			continue
		}
		out = append(out, federation.StatusSource{Manifest: manifest, Run: func(ctx context.Context) (*control.Status, *federationv1.SourceFailure) {
			if source.Crawler == nil {
				return nil, federation.FailureForError(manifest, "status", errors.New("status command has no crawler"))
			}
			status, err := r.sourceExecutor().Status(ctx, source.Crawler)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, "status", err)
			}
			return status, nil
		}})
	}
	return out
}

func (r *Runtime) federationSearchSources(sources []Source) []federation.SearchSource {
	out := make([]federation.SearchSource, 0, len(sources))
	for _, source := range sources {
		source := source
		manifest := cloneManifest(source.Manifest)
		if source.MetadataErr != nil {
			out = append(out, federation.SearchSource{Manifest: manifest, Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
				return trawlkit.SearchResult{}, federation.FailureForError(manifest, "search", source.MetadataErr)
			}})
			continue
		}
		if _, ok := manifest.Commands["search"]; !ok {
			out = append(out, federation.SearchSource{Manifest: manifest, SkipReason: "Search is not supported."})
			continue
		}
		out = append(out, federation.SearchSource{Manifest: manifest, Run: func(ctx context.Context, query trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			_, ok := source.Crawler.(trawlkit.Searcher)
			if !ok {
				return trawlkit.SearchResult{}, federation.FailureForError(manifest, "search", errors.New("declared search command has no searcher"))
			}
			result, err := r.sourceExecutor().Search(ctx, source.Crawler, query)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return trawlkit.SearchResult{}, federation.FailureForError(manifest, "search", err)
			}
			return result, nil
		}})
	}
	return out
}

func (r *Runtime) federationOpenSources(sources []Source) []federation.OpenSource {
	out := make([]federation.OpenSource, 0, len(sources))
	for _, source := range sources {
		source := source
		manifest := cloneManifest(source.Manifest)
		if source.MetadataErr != nil {
			out = append(out, federation.OpenSource{Manifest: manifest, Run: func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
				return nil, federation.FailureForError(manifest, "open", source.MetadataErr)
			}})
			continue
		}
		if _, ok := manifest.Commands["open"]; !ok {
			out = append(out, federation.OpenSource{Manifest: manifest, SkipReason: "Open is not supported."})
			continue
		}
		out = append(out, federation.OpenSource{Manifest: manifest, Run: func(ctx context.Context, ref, anchorID string) (*openv1.OpenRecord, *federationv1.SourceFailure) {
			if _, ok := source.Crawler.(trawlkit.RecordOpener); !ok {
				return nil, federation.FailureForError(manifest, "open", errors.New("declared open command has no record opener"))
			}
			record, err := r.sourceExecutor().OpenRecord(ctx, source.Crawler, ref, anchorID)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, "open", err)
			}
			return record, nil
		}})
	}
	return out
}

func cloneManifest(manifest control.Manifest) control.Manifest {
	copy := manifest
	copy.Aliases = append([]string(nil), manifest.Aliases...)
	copy.Headlines = append([]string(nil), manifest.Headlines...)
	copy.Capabilities = append([]string(nil), manifest.Capabilities...)
	copy.Commands = cloneCommands(manifest.Commands)
	return copy
}

func cloneCommands(commands map[string]control.Command) map[string]control.Command {
	if commands == nil {
		return nil
	}
	copy := make(map[string]control.Command, len(commands))
	for key, command := range commands {
		command.Argv = append([]string(nil), command.Argv...)
		command.Flags = append([]control.Flag(nil), command.Flags...)
		copy[key] = command
	}
	return copy
}
