package qa

import (
	"os"
	"path/filepath"
)

func createBirdDump(root string) error {
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"account.js": `window.YTD.account.part0 = [
  {"account":{"email":"alex@example.com","username":"fixture_alex","accountId":"100","createdAt":"2020-01-01T00:00:00.000Z","accountDisplayName":"Alex Example"}}
];`,
		"tweets.js": `window.YTD.tweets.part0 = [
  {"tweet":{"id":"1800000000000000001","id_str":"1800000000000000001","created_at":"Mon Jun 01 12:00:00 +0000 2026","full_text":"Synthetic launch note beside the canal.","user_id":"100","screen_name":"fixture_alex","name":"Alex Example","conversation_id":"1800000000000000001","favorite_count":"8","retweet_count":"2","reply_count":"3"}},
  {"tweet":{"id":"1800000000000000002","id_str":"1800000000000000002","created_at":"Mon Jun 01 12:05:00 +0000 2026","full_text":"A second synthetic launch reply.","user_id":"100","screen_name":"fixture_alex","name":"Alex Example","in_reply_to_status_id_str":"1800000000000000001","conversation_id":"1800000000000000001","favorite_count":"4","retweet_count":"1","reply_count":"0"}}
];`,
		"like.js": `window.YTD.like.part0 = [
  {"like":{"tweetId":"1800000000000000100","fullText":"A liked synthetic launch post.","createdAt":"2026-06-02T08:00:00Z"}}
];`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}
