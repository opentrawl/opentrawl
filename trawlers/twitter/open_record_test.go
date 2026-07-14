package birdcrawl

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	twitteropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/twitter/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previousLocal })
	input := store.OpenResult{
		Tweet:     store.Tweet{ID: "tweet-2", CreatedAt: time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC), AuthorID: "owner", AuthorHandle: "avery", AuthorName: "Avery Example", Text: "RT @example synthetic text", InReplyToID: "tweet-1", ConversationID: "conversation-1", QuotedTweetID: "quoted-1", LikeCount: 4, RetweetCount: 2, ReplyCount: 1, MetricsFetchedAt: time.Date(2026, 7, 10, 15, 0, 0, 0, time.UTC)},
		Ancestors: []store.OpenTweet{{Available: false, Ref: "twitter:tweet/tweet-1", Text: "unavailable (not in archive)"}},
		Replies:   []store.Tweet{{ID: "tweet-3", AuthorHandle: "morgan", AuthorName: "Morgan Example", Text: "Synthetic reply."}}, AncestorsTruncated: true, RepliesTruncated: true,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	value := openValue{result: input, aliases: map[string]string{"twitter:tweet/tweet-2": "a1b2c3"}, ownerAuthorID: "owner"}
	record := projectOpenRecord(value)
	if record.Ref != "twitter:tweet/tweet-2" || record.Tweet.GetWho() != "me (@avery)" || record.Tweet.GetInReplyTo() != "twitter:tweet/tweet-1" {
		t.Fatalf("tweet = %#v", record.Tweet)
	}
	if len(record.Ancestors) != 1 || !record.Ancestors[0].GetUnavailable() {
		t.Fatalf("ancestor = %#v", record.Ancestors)
	}
	name := string(record.ProtoReflect().Descriptor().FullName())
	if name != "trawl.source.twitter.open.v1.TwitterRecord" {
		t.Fatalf("message name = %q", name)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/trawl.source.twitter.open.v1.TwitterRecord" {
		t.Fatal("type URL changed")
	}
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	for _, fragment := range []string{`"ref":"twitter:tweet/tweet-2"`, `"note":"X archives retweets as a truncated stub; open the original on x.com."`, `"unavailable":true`, `"ancestors_truncated":true`, `"replies_truncated":true`} {
		if !strings.Contains(string(data), fragment) {
			t.Fatalf("ProtoJSON missing %q: %s", fragment, data)
		}
	}
	if strings.Contains(string(data), "a1b2c3") || strings.Contains(text, "a1b2c3") {
		t.Fatal("short alias leaked into source record")
	}
	assertExactRecord(t, record, &twitteropenv1.TwitterRecord{}, `{"ref":"twitter:tweet/tweet-2","tweet":{"ref":"twitter:tweet/tweet-2","time":"2026-07-10T14:00:00Z","who":"me (@avery)","text":"RT @example synthetic text","in_reply_to":"twitter:tweet/tweet-1","like_count":"4","retweet_count":"2","reply_count":"1","counts_as_of":"2026-07-10T15:00:00Z","note":"X archives retweets as a truncated stub; open the original on x.com.","conversation_id":"conversation-1","quoted_tweet_id":"quoted-1"},"ancestors":[{"ref":"twitter:tweet/tweet-1","text":"unavailable (not in archive)","unavailable":true}],"replies":[{"ref":"twitter:tweet/tweet-3","who":"Morgan Example (@morgan)","text":"Synthetic reply."}],"ancestors_truncated":true,"replies_truncated":true}`)
	aliases := map[string]string{"twitter:tweet/tweet-2": "a1b2c3"}
	ownerAuthorID := "owner"
	presentation := projectOpenPresentation(openValue{result: input, aliases: aliases, ownerAuthorID: ownerAuthorID})
	if presentation.Title != "me (@avery)" || len(presentation.Blocks) != 6 || len(presentation.Facts) != 2 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	evidenceInput := struct {
		Result        store.OpenResult `json:"result"`
		OwnerAuthorID string           `json:"owner_author_id"`
	}{input, ownerAuthorID}
	assertOpenPresentation(t, "twitter", evidenceInput, record, presentation)
	assertExactPresentation(t, presentation, `title: "me (@avery)"
blocks: { fields: { fields: { label: "Time" display: "10 July 2026 at 14:00" } fields: { label: "Likes" display: "4" } fields: { label: "Reposts" display: "2" } fields: { label: "Replies" display: "1" } fields: { label: "Counts as of" display: "10 July 2026 at 15:00" } } }
blocks: { prose: { text: "RT @example synthetic text" } anchor_id: "match" }
blocks: { heading: { text: "Ancestors" } }
blocks: { table: { columns: "Time" columns: "From" columns: "Text" rows: { role: ROLE_NORMAL cells: {} cells: {} cells: { display: "unavailable (not in archive)" } } } }
blocks: { heading: { text: "Replies" } }
blocks: { table: { columns: "Time" columns: "From" columns: "Text" rows: { role: ROLE_NORMAL cells: {} cells: { display: "Morgan Example (@morgan)" } cells: { display: "Synthetic reply." } } } }
facts: { kind: KIND_TRUNCATION message: "Earlier conversation context is truncated." }
facts: { kind: KIND_TRUNCATION message: "Replies are truncated." }
primary_anchor_id: "match"`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Tweet.AuthorName = ""
		blank.Tweet.AuthorHandle = ""
		if got := projectOpenPresentation(openValue{result: blank, ownerAuthorID: "other"}).Title; got != "Post" {
			t.Fatalf("title = %q", got)
		}
	})
}

func assertExactRecord(t *testing.T, got, want proto.Message, wantJSON string) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(wantJSON), want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("record = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
	if prototext.Format(got) != prototext.Format(want) {
		t.Fatal("protobuf text changed")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var actualCompact, wantCompact bytes.Buffer
	if err := json.Compact(&actualCompact, data); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&wantCompact, []byte(wantJSON)); err != nil {
		t.Fatal(err)
	}
	if actualCompact.String() != wantCompact.String() {
		t.Fatalf("ProtoJSON = %s\nwant = %s", data, wantJSON)
	}
}

func assertOpenPresentation(t *testing.T, source string, input any, machine interface {
	proto.Message
	GetRef() string
}, presentation *presentationv1.PresentationDocument) {
	t.Helper()
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	open := &openv1.OpenRecord{SourceId: source, OpenRef: machine.GetRef(), Data: packed, Presentation: presentation}
	if err := openrecord.Validate(open); err != nil {
		t.Fatal(err)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, "input.json", inputJSON)
	writeEvidence(t, source, "record.pbtxt", []byte(prototext.Format(machine)))
	writeEvidence(t, source, "presentation.pbtxt", []byte(prototext.Format(presentation)))
	writeEvidence(t, source, "validated-open.pbtxt", []byte(prototext.Format(open)))
}

func writeEvidence(t *testing.T, source, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	directory = filepath.Join(directory, source)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func writeRuntimeOpenEvidence(t *testing.T, source, caseName, ref string, loaded any, record *openv1.OpenRecord) {
	t.Helper()
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "argv-ref.txt"), []byte("OpenRecord "+ref+"\n"))
	loadedJSON, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "loaded-value.json"), append(loadedJSON, '\n'))
	writeEvidence(t, source, filepath.Join(caseName, "machine.pbtxt"), []byte(prototext.Format(machine)))
	writeEvidence(t, source, filepath.Join(caseName, "presentation.pbtxt"), []byte(prototext.Format(record.Presentation)))
	writeEvidence(t, source, filepath.Join(caseName, "validated-open.pbtxt"), []byte(prototext.Format(record)))
}

func writeLegacyOpenEvidence(t *testing.T, source, caseName, format string, stdout []byte, err error) {
	t.Helper()
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stdout.txt"), stdout)
	writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stderr.txt"), nil)
	if err == nil {
		writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("0\n"))
		writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), nil)
		return
	}
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("1\n"))
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), []byte(err.Error()+"\n"))
}

func assertLegacyOpenGolden(t *testing.T, stdout []byte, err error, wantSHA256 string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(stdout)); got != wantSHA256 {
		t.Fatalf("legacy open stdout SHA-256 = %s, want %s", got, wantSHA256)
	}
}

func writeRawEvidence(t *testing.T, source, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	directory = filepath.Join(directory, source)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, name)), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func assertExactPresentation(t *testing.T, got *presentationv1.PresentationDocument, wantText string) {
	t.Helper()
	want := &presentationv1.PresentationDocument{}
	if err := prototext.Unmarshal([]byte(wantText), want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) || prototext.Format(got) != prototext.Format(want) {
		t.Fatalf("presentation = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
}
