package birdcrawl

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	twitteropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/twitter/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
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
	record := projectOpenRecord(input, map[string]string{"twitter:tweet/tweet-2": "a1b2c3"}, "owner")
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
