package archive

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

// note-tweet.js holds the full text of long-form posts; the matching entry in
// tweets.js is truncated with a trailing ellipsis. The dump gives no tweet id
// on the note, so matching is mechanical: creation time within one second and
// the truncated text being a prefix of the note text. Three dump quirks are
// normalised away, each verified against a real dump: leading reply @mentions
// appear inconsistently on both sides, t.co ids differ for the same link
// between the two files, and text is HTML-escaped in tweets.js. The rule
// matched 455/455 notes with zero ambiguity on the 2026-05-29 reference dump.

type noteTweetWrapper struct {
	NoteTweet rawNoteTweet `json:"noteTweet"`
}

type rawNoteTweet struct {
	CreatedAt string        `json:"createdAt"`
	Core      rawNoteCore   `json:"core"`
	Lifecycle rawNoteStatus `json:"lifecycle"`
}

type rawNoteCore struct {
	Text string `json:"text"`
}

type rawNoteStatus struct {
	Name string `json:"name"`
}

type noteTweet struct {
	createdAt time.Time
	text      string
}

func parseNoteTweets(data []byte) ([]noteTweet, error) {
	if len(data) == 0 {
		return nil, nil
	}
	body, err := unwrapYTD(data)
	if err != nil {
		return nil, err
	}
	var wrapped []noteTweetWrapper
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("parse note-tweet.js: %w", err)
	}
	notes := make([]noteTweet, 0, len(wrapped))
	for _, item := range wrapped {
		text := strings.TrimSpace(html.UnescapeString(item.NoteTweet.Core.Text))
		if text == "" {
			continue
		}
		createdAt, err := parseTweetTime(item.NoteTweet.CreatedAt)
		if err != nil || createdAt.IsZero() {
			continue
		}
		notes = append(notes, noteTweet{createdAt: createdAt, text: text})
	}
	return notes, nil
}

// mergeNoteTweets replaces truncated authored text with the full note text.
// Anything ambiguous or unmatched is counted, never guessed.
func mergeNoteTweets(tweets []store.Tweet, notes []noteTweet) (merged, unmatched int) {
	bySecond := make(map[int64][]int)
	for i, tweet := range tweets {
		if !tweet.CreatedAt.IsZero() {
			bySecond[tweet.CreatedAt.Unix()] = append(bySecond[tweet.CreatedAt.Unix()], i)
		}
	}
	for _, note := range notes {
		noteKey := dropShortLinks(stripLeadingMentions(note.text))
		match := -1
		ambiguous := false
		for _, delta := range []int64{0, 1, -1} {
			for _, i := range bySecond[note.createdAt.Unix()+delta] {
				base := noteMatchBase(tweets[i].Text)
				if base == "" || !strings.HasPrefix(noteKey, dropShortLinks(base)) {
					continue
				}
				if match >= 0 && match != i {
					ambiguous = true
				}
				match = i
			}
		}
		if match < 0 || ambiguous {
			unmatched++
			continue
		}
		if len(note.text) > len(tweets[match].Text) {
			tweets[match].Text = note.text
		}
		merged++
	}
	return merged, unmatched
}

// noteMatchBase reduces a tweets.js text to the part comparable with a note:
// everything before the truncation ellipsis and the first t.co link, without
// leading reply mentions.
func noteMatchBase(tweetText string) string {
	base := tweetText
	if idx := strings.Index(base, "…"); idx >= 0 {
		base = base[:idx]
	}
	base = stripLeadingMentions(base)
	if idx := strings.Index(base, "https://t.co/"); idx >= 0 {
		base = base[:idx]
	}
	return strings.TrimSpace(base)
}

var leadingMentions = regexp.MustCompile(`^(@\w+\s+)+`)
var shortLinks = regexp.MustCompile(`https://t\.co/\S+`)

func stripLeadingMentions(s string) string {
	return leadingMentions.ReplaceAllString(strings.TrimSpace(s), "")
}

func dropShortLinks(s string) string {
	return shortLinks.ReplaceAllString(s, "")
}
