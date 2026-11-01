package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// ReferenceOfAgentConversationRunningThisProcess names the conversation of
// the agent that started this process, from the identifiers its harness puts
// in the environment, as this trawler archives it. It is empty outside a
// known harness.
func ReferenceOfAgentConversationRunningThisProcess() *trawlkit.CanonicalArchiveRecordReference {
	if threadIdentifier := strings.TrimSpace(os.Getenv("T3CODE_THREAD_ID")); threadIdentifier != "" {
		return trawlkit.NewCanonicalArchiveRecordReference("agents:t3-code-thread/" + threadIdentifier)
	}
	if threadIdentifier := strings.TrimSpace(os.Getenv("CODEX_THREAD_ID")); threadIdentifier != "" {
		return trawlkit.NewCanonicalArchiveRecordReference("agents:codex-thread/" + threadIdentifier)
	}
	if sessionIdentifier := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SESSION_ID")); sessionIdentifier != "" {
		return trawlkit.NewCanonicalArchiveRecordReference("agents:claude-code-session/" + sessionIdentifier)
	}
	return nil
}

// TrawlLinksForArchivedAgentConversations returns the link of each archived
// conversation among those given. A conversation the archive does not hold
// yet has none.
func TrawlLinksForArchivedAgentConversations(
	ctx context.Context,
	stateRoot string,
	references []*trawlkit.CanonicalArchiveRecordReference,
) (map[string]*trawlkit.GloballyRoutableTrawlLink, error) {
	links := map[string]*trawlkit.GloballyRoutableTrawlLink{}
	archivePath := filepath.Join(stateRoot, "agents", "agents.db")
	if _, err := os.Stat(archivePath); errors.Is(err, os.ErrNotExist) {
		return links, nil
	}
	archive, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = archive.Close() }()
	var referenceTexts []string
	for _, reference := range references {
		referenceTexts = append(referenceTexts, trawlkit.CanonicalArchiveRecordReferenceText(reference))
	}
	aliases, err := shortref.NewSQLiteIndex(archive.DB()).Aliases(ctx, referenceTexts)
	if err != nil {
		return nil, err
	}
	for referenceText, alias := range aliases {
		link, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity("agents"),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(alias),
		})
		if err != nil {
			return nil, err
		}
		links[referenceText] = link
	}
	return links, nil
}
