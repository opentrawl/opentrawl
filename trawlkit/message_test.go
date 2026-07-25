package trawlkit

import (
	"encoding/json"
	"testing"
)

func TestNewMessageListKeepsStableEmptyArrayAndExactCompleteness(t *testing.T) {
	value := NewMessageList(nil, 2)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"messages":[],"total":2,"truncated":true}`; got != want {
		t.Fatalf("message list JSON = %s, want %s", got, want)
	}
}
