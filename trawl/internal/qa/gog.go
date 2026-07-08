package qa

import "os"

func writeFakeGog(path string) error {
	return os.WriteFile(path, []byte(fakeGogScript), 0o755)
}

const fakeGogScript = `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'v0.31.1 (test 2026-07-02T00:00:00Z)\n'
  exit 0
fi

if [ "$1" = "auth" ] && [ "$2" = "list" ]; then
  printf 'alice@example.com\tmain\tgmail\t2030-01-02T03:04:05Z\ttrue\t\toauth\n'
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "init" ]; then
  repo=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--repo" ]; then
      repo="$2"
      shift 2
      continue
    fi
    shift
  done
  mkdir -p "$repo/.git"
  printf '[core]\n\trepositoryformatversion = 0\n' > "$repo/.git/config"
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "gmail" ] && [ "$3" = "push" ]; then
  repo=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--repo" ]; then
      repo="$2"
      shift 2
      continue
    fi
    shift
  done
  mkdir -p "$repo"
  cat > "$repo/manifest.json" <<'JSON'
{"services":{"gmail":{"shards":[
{"path":"data/gmail/account/labels.jsonl.gz.age","plaintext_sha256":"labels-hash","rows":1},
{"path":"data/gmail/account/messages/part-000001.jsonl.gz.age","plaintext_sha256":"messages-hash","rows":3}
]}}}
JSON
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "cat" ]; then
  shard=""
  for arg in "$@"; do
    case "$arg" in
      *.jsonl.gz.age) shard="$arg" ;;
    esac
  done
  case "$shard" in
    *labels.jsonl.gz.age)
      printf '{"id":"INBOX","name":"Inbox","type":"system"}\n'
      ;;
    *part-000001.jsonl.gz.age)
      cat <<'JSON'
{"id":"m3","threadId":"t3","historyId":"h3","internalDate":1783000991000,"labelIds":["INBOX"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE5ld2VzdCBsYXVuY2ggcHJvamVjdCBzeW5jDQoNCk5ld2VzdCBsYXVuY2ggcHJvamVjdCBzeW5jIGJvZHkuDQo="}
{"id":"m2","threadId":"t2","historyId":"h2","internalDate":1782997391000,"labelIds":["SENT"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE1pZGRsZSBsYXVuY2ggcHJvamVjdCBzeW5jDQoNCk1pZGRsZSBsYXVuY2ggcHJvamVjdCBzeW5jIGJvZHkuDQo="}
{"id":"m1","threadId":"t1","historyId":"h1","internalDate":1782993791000,"labelIds":["ARCHIVE"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE9sZCBsYXVuY2ggcHJvamVjdCBzeW5jDQoNCk9sZCBsYXVuY2ggcHJvamVjdCBzeW5jIGJvZHkuDQo="}
JSON
      ;;
  esac
  exit 0
fi

if [ "$1" = "contacts" ] && [ "$2" = "list" ]; then
  cat <<'JSON'
{"contacts":[{"resource":"people/c1","name":"Alice Example","phone":"+15550101000"},{"resource":"people/c2","name":"Bob Example","phone":""}],"nextPageToken":""}
JSON
  exit 0
fi

exit 1
`
