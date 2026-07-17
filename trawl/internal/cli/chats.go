package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type ChatsCmd struct {
	With   string `name:"with" help:"Only conversations with a participant whose name matches"`
	Unread bool   `name:"unread" help:"Only conversations with unread messages"`
	Limit  int    `name:"limit" default:"50" help:"Maximum conversations to show"`
	All    bool   `name:"all" help:"Show every conversation"`
}

type federatedChat struct {
	Source           string   `json:"source"`
	Surface          string   `json:"surface"`
	ID               string   `json:"id"`
	Ref              string   `json:"ref,omitempty"`
	Chat             string   `json:"chat,omitempty"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Participants     *int64   `json:"participants,omitempty"`
	ParticipantNames []string `json:"participant_names,omitempty"`
	LastActivity     string   `json:"last_activity,omitempty"`
	Unread           *int64   `json:"unread,omitempty"`

	displayID    string
	activityTime time.Time
}

type federatedChatsOutput struct {
	Chats              []federatedChat `json:"chats"`
	Truncated          bool            `json:"truncated"`
	UnavailableSources []string        `json:"unavailable_sources,omitempty"`
	FailedSources      []failedSource  `json:"failed_sources,omitempty"`
	successfulSources  int
}

type chatSourceResult struct {
	source    Source
	chats     []federatedChat
	truncated bool
	err       error
}

func (c *ChatsCmd) Run(r *Runtime) error {
	if !c.All && c.Limit < 1 {
		return usageErr{errors.New("--limit must be at least 1 (or use --all)")}
	}
	installed := discoverCrawlers(r.ctx)
	sources := chatSources(installed)
	aliases := r.resolveChatPersonAliases(installed, c.With)
	results := make(chan chatSourceResult, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.listSourceChats(source, *c, aliases)
		}()
	}
	wg.Wait()
	close(results)

	output := federatedChatsOutput{Chats: []federatedChat{}}
	for result := range results {
		if result.err != nil {
			var missing trawlkit.MissingArchiveError
			if errors.As(result.err, &missing) {
				output.UnavailableSources = append(output.UnavailableSources, result.source.ID)
				continue
			}
			remedy := ""
			if errors.Is(result.err, trawlkit.ErrChatsNoReadState) {
				remedy = "run trawl sync " + sourceCommandToken(result.source)
			}
			output.FailedSources = append(output.FailedSources, failedSource{
				Source:       result.source.ID,
				Reason:       failureReason(result.err),
				displayName:  sourceHumanName(result.source),
				commandToken: sourceCommandToken(result.source),
				remedy:       remedy,
			})
			continue
		}
		output.successfulSources++
		output.Truncated = output.Truncated || result.truncated
		output.Chats = append(output.Chats, result.chats...)
	}
	sort.SliceStable(output.Chats, func(i, j int) bool {
		return output.Chats[i].activityTime.After(output.Chats[j].activityTime)
	})
	if !c.All && len(output.Chats) > c.Limit {
		output.Chats = output.Chats[:c.Limit]
		output.Truncated = true
	}
	sort.Slice(output.FailedSources, func(i, j int) bool { return output.FailedSources[i].Source < output.FailedSources[j].Source })
	sort.Strings(output.UnavailableSources)

	if r.root.JSON {
		if err := writeJSON(r.stdout, output); err != nil {
			return err
		}
	} else if err := renderFederatedChats(r, output, c.Unread); err != nil {
		return err
	}
	if !r.root.JSON {
		for _, failure := range output.FailedSources {
			r.reportFailedSourceFailure(failure, "chats", r.reasonDetail(failure.Reason))
		}
	}
	return partialFailureExit(len(output.FailedSources), len(sources)-len(output.FailedSources)-len(output.UnavailableSources))
}

func chatSources(sources []Source) []Source {
	out := make([]Source, 0, len(sources))
	for _, source := range sources {
		if source.MetadataErr != nil || !hasCapability(source, "chats") {
			continue
		}
		if _, ok := source.Crawler.(trawlkit.ChatLister); ok {
			out = append(out, source)
		}
	}
	return out
}

func (r *Runtime) listSourceChats(source Source, command ChatsCmd, aliases []string) chatSourceResult {
	result := chatSourceResult{source: source}
	query := trawlkit.ChatQuery{With: command.With, WithAliases: aliases, Unread: command.Unread, Limit: command.Limit, All: command.All}
	started := r.logSourceStart(source, "chats")
	chatResult, err := trawlkit.RunChats(r.ctx, source.Crawler, query, trawlkit.ChatsRunOptions{
		Timeout:   r.timeout,
		Verbosity: r.verbosity(),
		Stderr:    r.lockedStderr(),
	})
	if errors.Is(err, context.DeadlineExceeded) {
		err = sourceTimeout("chats")
	}
	result.err = err
	if err == nil {
		result.truncated = chatResult.Truncated
		result.chats = make([]federatedChat, 0, len(chatResult.Chats))
		for _, chat := range chatResult.Chats {
			result.chats = append(result.chats, federatedChat{
				Source:           source.ID,
				Surface:          sourceHumanName(source),
				ID:               chat.ID,
				Ref:              chat.Ref,
				Chat:             chatResult.ShortRefs[chat.Ref],
				Name:             federatedChatName(chat),
				Kind:             federatedChatKind(chat),
				Participants:     copyFederatedCount(chat.Participants),
				ParticipantNames: append([]string(nil), chat.ParticipantNames...),
				LastActivity:     federatedChatTime(chat.LastActivity),
				Unread:           copyFederatedCount(chat.Unread),
				displayID:        chat.DisplayID,
				activityTime:     chat.LastActivity,
			})
		}
	}
	r.logSourceDone(source, "chats", started, result.err, "chats="+fmt.Sprint(len(result.chats)))
	return result
}

// resolveChatPersonAliases asks the Contacts People index whether --with names
// one unambiguous person. The index is the authority for cross-service identity;
// messaging sources remain authorities for their own chat lists. We accept only
// a unique best match and reject close-spelling-only matches. If Contacts is
// absent, unreadable or ambiguous, chats keeps its existing literal filter
// instead of guessing or making a healthy messaging archive unusable.
func (r *Runtime) resolveChatPersonAliases(installed []Source, query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	contacts, ok := findSource(installed, "contacts")
	if !ok || contacts.MetadataErr != nil {
		return nil
	}
	matcher, ok := contacts.Crawler.(trawlkit.WhoMatcher)
	if !ok {
		return nil
	}
	var candidates []whomatch.Candidate
	err := r.withSourceRequest(contacts, "who", sourceStoreRead, ckoutput.JSON, io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		var err error
		candidates, err = matcher.Who(ctx, req, query)
		return err
	})
	if err != nil {
		return nil
	}
	candidate, ok := uniqueBestChatPerson(candidates, query)
	if !ok {
		return nil
	}
	aliases := make([]string, 0, 1+len(candidate.Aliases)+len(candidate.Identifiers))
	aliases = append(aliases, candidate.Who)
	aliases = append(aliases, candidate.Aliases...)
	aliases = append(aliases, candidate.Identifiers...)
	return aliases
}

func uniqueBestChatPerson(candidates []whomatch.Candidate, query string) (whomatch.Candidate, bool) {
	var best whomatch.Candidate
	var bestRank whomatch.Rank
	bestCount := 0
	for _, candidate := range candidates {
		rank, ok := candidate.MatchRank(query)
		if !ok || rank == whomatch.RankCloseSpelling {
			continue
		}
		switch {
		case rank.BetterThan(bestRank):
			best = candidate
			bestRank = rank
			bestCount = 1
		case rank == bestRank:
			bestCount++
		}
	}
	return best, bestCount == 1
}

func renderFederatedChats(r *Runtime, output federatedChatsOutput, unread bool) error {
	if len(output.Chats) == 0 {
		if len(output.FailedSources) > 0 {
			_, err := fmt.Fprintln(r.stdout, "No chats could be listed.")
			return err
		}
		if len(output.UnavailableSources) > 0 && output.successfulSources == 0 {
			_, err := fmt.Fprintln(r.stdout, "No messaging archives found. Run trawl sync to create them.")
			return err
		}
		label := "No chats."
		if unread {
			label = "No unread chats."
		}
		_, err := fmt.Fprintln(r.stdout, label)
		return err
	}
	heading := "Chats"
	if unread {
		heading = "Unread chats"
	}
	if _, err := fmt.Fprintf(r.stdout, "%s: showing %s across messaging sources, newest first.\n", heading, render.FormatInteger(int64(len(output.Chats)))); err != nil {
		return err
	}
	if output.Truncated {
		if _, err := fmt.Fprintln(r.stdout, "More: raise --limit, or list all with --all"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	showParticipants := false
	showUnread := false
	for _, chat := range output.Chats {
		showParticipants = showParticipants || strings.TrimSpace(federatedParticipantPreview(chat.ParticipantNames, chat.Participants)) != ""
		showUnread = showUnread || chat.Unread != nil
	}
	columns := []render.TableColumn{{Header: "source"}, {Header: "name"}, {Header: "kind"}}
	if showParticipants {
		columns = append(columns, render.TableColumn{Header: "participants"})
	}
	if showUnread {
		columns = append(columns, render.TableColumn{Header: "unread", AlignRight: true})
	}
	columns = append(columns, render.TableColumn{Header: "last"}, render.TableColumn{Header: "chat"})
	rows := make([][]string, 0, len(output.Chats))
	for _, chat := range output.Chats {
		row := []string{chat.Surface, chat.Name, chat.Kind}
		if showParticipants {
			row = append(row, federatedParticipantPreview(chat.ParticipantNames, chat.Participants))
		}
		if showUnread {
			row = append(row, federatedCountText(chat.Unread))
		}
		row = append(row, federatedChatLastText(chat.activityTime), firstNonEmpty(chat.Chat, chat.displayID))
		rows = append(rows, row)
	}
	return render.WriteTable(r.stdout, columns, rows)
}

func federatedChatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func federatedChatLastText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return render.ShortLocalTime(value)
}

func federatedChatName(chat trawlkit.Chat) string {
	if name := strings.TrimSpace(chat.Title); name != "" {
		return name
	}
	if len(chat.ParticipantNames) > 0 && strings.TrimSpace(chat.ParticipantNames[0]) != "" {
		return strings.TrimSpace(chat.ParticipantNames[0])
	}
	if chat.Group {
		return "group chat"
	}
	return "chat"
}

func federatedChatKind(chat trawlkit.Chat) string {
	if chat.Group {
		return "group"
	}
	return "dm"
}

func federatedParticipantPreview(names []string, total *int64) string {
	clean := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			clean = append(clean, name)
		}
	}
	shown := clean
	if len(shown) > 3 {
		shown = shown[:3]
	}
	remaining := len(clean) - len(shown)
	if total != nil && int(*total) > len(clean) {
		remaining = int(*total) - len(shown)
	}
	text := strings.Join(shown, ", ")
	if remaining > 0 {
		text += " +" + render.FormatInteger(int64(remaining))
	}
	return text
}

func federatedCountText(value *int64) string {
	if value == nil {
		return ""
	}
	return render.FormatInteger(*value)
}

func copyFederatedCount(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func partialFailureExit(failures, successes int) error {
	if failures == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}
