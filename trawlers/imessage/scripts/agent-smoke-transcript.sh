#!/usr/bin/env bash
set -u

search_query=""
max_all_messages=25
out_dir=""
preview_bytes=4000
inline_raw=0

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-smoke-transcript.sh [--query TEXT] [--max-all-messages N] [--preview-bytes N] [--inline-raw] [--out-dir DIR]

Runs iMessage through `trawl imessage` and captures exact stdout/stderr for a
progressive agent smoke pass. Exact raw outputs are written only to the local
output directory, which defaults to /tmp. The default review file contains
bounded previews plus raw file paths, not a full giant transcript.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --query|--search-query)
      if [[ $# -lt 2 ]]; then
        echo "--query requires a value" >&2
        exit 2
      fi
      search_query=$2
      shift 2
      ;;
    --max-all-messages)
      if [[ $# -lt 2 ]]; then
        echo "--max-all-messages requires a value" >&2
        exit 2
      fi
      max_all_messages=$2
      shift 2
      ;;
    --preview-bytes)
      if [[ $# -lt 2 ]]; then
        echo "--preview-bytes requires a value" >&2
        exit 2
      fi
      preview_bytes=$2
      shift 2
      ;;
    --inline-raw)
      inline_raw=1
      shift
      ;;
    --out-dir)
      if [[ $# -lt 2 ]]; then
        echo "--out-dir requires a value" >&2
        exit 2
      fi
      out_dir=$2
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if ! command -v trawl >/dev/null 2>&1; then
  echo "trawl not found on PATH; run scripts/dev-bin first" >&2
  exit 127
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq not found on PATH" >&2
  exit 127
fi

if [[ -z "$out_dir" ]]; then
  out_dir="${TMPDIR:-/tmp}/imsgcrawl-agent-smoke-$(date -u +%Y%m%dT%H%M%SZ)-$$"
fi
raw_dir="$out_dir/raw"
synthetic_home="$out_dir/home"
mkdir -p "$raw_dir" "$synthetic_home"
real_home="${HOME:-}"
if [[ -z "$real_home" ]]; then
  echo "HOME is not set" >&2
  exit 2
fi
mkdir -p "$synthetic_home/Library"
messages_link="$synthetic_home/Library/Messages"
if [[ ! -e "$messages_link" && ! -L "$messages_link" ]]; then
  ln -s "$real_home/Library/Messages" "$messages_link"
fi
addressbook_source="$real_home/Library/Application Support/AddressBook"
addressbook_link="$synthetic_home/Library/Application Support/AddressBook"
if [[ -d "$addressbook_source" && ! -e "$addressbook_link" && ! -L "$addressbook_link" ]]; then
  mkdir -p "$synthetic_home/Library/Application Support"
  ln -s "$addressbook_source" "$addressbook_link"
fi

review="$out_dir/review.txt"
transcript="$out_dir/transcript.txt"
commands_tsv="$out_dir/commands.tsv"
manifest_jsonl="$out_dir/manifest.jsonl"
failures=0
step=0
last_stdout=""

quote_command() {
  printf '%q ' "$@"
}

append_preview() {
  local label=$1
  local path=$2
  local bytes
  bytes=$(wc -c <"$path" | tr -d ' ')
  {
    printf '\n----- %s PREVIEW BEGIN -----\n' "$label"
    printf 'file: %s\nbytes: %s\n' "$path" "$bytes"
    if [[ "$bytes" -eq 0 ]]; then
      printf '(empty)\n'
    elif [[ "$bytes" -le "$preview_bytes" ]]; then
      cat "$path"
      printf '\n'
    else
      local head_bytes=$((preview_bytes / 2))
      local tail_bytes=$((preview_bytes - head_bytes))
      printf 'preview_truncated: true\n'
      printf 'Open the raw file above for exact full output.\n\n'
      head -c "$head_bytes" "$path"
      printf '\n... PREVIEW TRUNCATED: %s bytes total ...\n' "$bytes"
      tail -c "$tail_bytes" "$path"
      printf '\n'
    fi
    printf -- '----- %s PREVIEW END -----\n' "$label"
  } >>"$review"
}

run_step() {
  local name=$1
  shift
  step=$((step + 1))
  local id
  id=$(printf '%03d' "$step")
  local slug
  slug=$(printf '%s' "$name" | tr -cs '[:alnum:]' '-' | sed 's/^-//; s/-$//' | tr '[:upper:]' '[:lower:]')
  local stdout_path="$raw_dir/$id-$slug.stdout"
  local stderr_path="$raw_dir/$id-$slug.stderr"

  "$@" >"$stdout_path" 2>"$stderr_path"
  local code=$?
  last_stdout="$stdout_path"
  local stdout_bytes
  local stderr_bytes
  stdout_bytes=$(wc -c <"$stdout_path" | tr -d ' ')
  stderr_bytes=$(wc -c <"$stderr_path" | tr -d ' ')

  {
    printf '\n================================================================================\n'
    printf '[%s] %s\n' "$id" "$name"
    printf '================================================================================\n'
    printf 'command: '
    quote_command "$@"
    printf '\n'
    printf 'exit_code: %s\n' "$code"
    printf 'stdout_file: %s\n' "$stdout_path"
    printf 'stderr_file: %s\n' "$stderr_path"
    printf 'stdout_bytes: %s\n' "$stdout_bytes"
    printf 'stderr_bytes: %s\n' "$stderr_bytes"
  } >>"$review"

  append_preview "STDOUT $id" "$stdout_path"
  append_preview "STDERR $id" "$stderr_path"

  if [[ "$inline_raw" -eq 1 ]]; then
    {
      printf '\n================================================================================\n'
      printf '[%s] %s\n' "$id" "$name"
      printf '================================================================================\n'
      printf 'command: '
      quote_command "$@"
      printf '\n'
      printf 'exit_code: %s\n' "$code"
      printf 'stdout_file: %s\n' "$stdout_path"
      printf 'stderr_file: %s\n' "$stderr_path"
      printf 'stdout_bytes: %s\n' "$stdout_bytes"
      printf 'stderr_bytes: %s\n' "$stderr_bytes"
      printf '\n----- STDOUT BEGIN %s -----\n' "$id"
      cat "$stdout_path"
      printf '\n----- STDOUT END %s -----\n' "$id"
      printf '\n----- STDERR BEGIN %s -----\n' "$id"
      cat "$stderr_path"
      printf '\n----- STDERR END %s -----\n' "$id"
    } >>"$transcript"
  fi

  printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$name" "$code" "$stdout_path" "$stderr_path" >>"$commands_tsv"
  jq -nc \
    --arg id "$id" \
    --arg name "$name" \
    --arg command "$(quote_command "$@")" \
    --argjson exit_code "$code" \
    --arg stdout_file "$stdout_path" \
    --arg stderr_file "$stderr_path" \
    --argjson stdout_bytes "$stdout_bytes" \
    --argjson stderr_bytes "$stderr_bytes" \
    '{id:$id,name:$name,command:$command,exit_code:$exit_code,stdout_file:$stdout_file,stderr_file:$stderr_file,stdout_bytes:$stdout_bytes,stderr_bytes:$stderr_bytes}' >>"$manifest_jsonl"

  if [[ "$code" -ne 0 ]]; then
    failures=$((failures + 1))
  fi
}

append_note() {
  {
    printf '\n================================================================================\n'
    printf 'NOTE\n'
    printf '================================================================================\n'
    printf '%s\n' "$1"
  } >>"$review"
  if [[ "$inline_raw" -eq 1 ]]; then
    {
      printf '\n================================================================================\n'
      printf 'NOTE\n'
      printf '================================================================================\n'
      printf '%s\n' "$1"
    } >>"$transcript"
  fi
}

cat >"$review" <<EOF
imsgcrawl Agent Smoke Review

Generated at: $(date -u +%Y-%m-%dT%H:%M:%SZ)
Trawl binary: $(command -v trawl)
Output directory: $out_dir
Raw output directory: $raw_dir
Synthetic HOME: $synthetic_home
Source links: Messages and optional AddressBook from the invoking HOME
Preview bytes per stream: $preview_bytes

This review contains bounded previews. Exact raw stdout/stderr for each command
are in the raw output directory and listed in manifest.jsonl/commands.tsv. Treat
all artifacts as private local crawler data. Do not commit them, paste them into
public systems, or send them off-machine without explicit user consent.
EOF

if [[ "$inline_raw" -eq 1 ]]; then
  cat >"$transcript" <<EOF
imsgcrawl Agent Smoke Transcript

Generated at: $(date -u +%Y-%m-%dT%H:%M:%SZ)
Trawl binary: $(command -v trawl)
Output directory: $out_dir
Synthetic HOME: $synthetic_home
Source links: Messages and optional AddressBook from the invoking HOME

This transcript intentionally contains exact local command output inline. Treat
it as private local crawler data. Do not commit it, paste it into public systems,
or send it off-machine without explicit user consent.
EOF
fi

: >"$commands_tsv"
: >"$manifest_jsonl"

run_step "version" trawl --version
run_step "source-help" trawl imessage
run_step "help-chats-flag" trawl imessage chats --help
run_step "help-messages-flag" trawl imessage messages --help
run_step "help-search-flag" trawl imessage search --help
run_step "help-open-flag" trawl imessage open --help
run_step "help-contacts-export-flag" trawl imessage contacts export --help

run_step "metadata-text" env HOME="$synthetic_home" trawl imessage metadata
run_step "metadata-json" env HOME="$synthetic_home" trawl --json imessage metadata

run_step "status-before-sync-text" env HOME="$synthetic_home" trawl imessage status
run_step "status-before-sync-json" env HOME="$synthetic_home" trawl --json imessage status
run_step "sync-text" env HOME="$synthetic_home" trawl imessage sync
run_step "sync-json" env HOME="$synthetic_home" trawl --json imessage sync
run_step "status-after-sync-text" env HOME="$synthetic_home" trawl imessage status
run_step "status-after-sync-json" env HOME="$synthetic_home" trawl --json imessage status

run_step "chats-text-default" env HOME="$synthetic_home" trawl imessage chats
run_step "chats-json-default" env HOME="$synthetic_home" trawl --json imessage chats
chats_json="$last_stdout"
run_step "chats-json-limit-one" env HOME="$synthetic_home" trawl --json imessage chats --limit 1

first_chat_id=$(jq -r '.items[0].chat_id // empty' "$chats_json" 2>/dev/null || true)
first_chat_count=$(jq -r '.items[0].message_count // empty' "$chats_json" 2>/dev/null || true)
small_chat_id=$(jq -r --argjson max "$max_all_messages" '[.items[] | select((.message_count // 0) > 0 and (.message_count // 0) <= $max)][0].chat_id // empty' "$chats_json" 2>/dev/null || true)
small_chat_count=$(jq -r --argjson max "$max_all_messages" '[.items[] | select((.message_count // 0) > 0 and (.message_count // 0) <= $max)][0].message_count // empty' "$chats_json" 2>/dev/null || true)

append_note "Selected first_chat_id=$first_chat_id first_chat_message_count=$first_chat_count small_chat_id=$small_chat_id small_chat_message_count=$small_chat_count max_all_messages=$max_all_messages."

if [[ -n "$first_chat_id" ]]; then
  run_step "messages-text-default-first-chat" env HOME="$synthetic_home" trawl imessage messages --chat "$first_chat_id"
  run_step "messages-json-default-first-chat" env HOME="$synthetic_home" trawl --json imessage messages --chat "$first_chat_id"
  run_step "messages-json-limit-three-first-chat" env HOME="$synthetic_home" trawl --json imessage messages --chat "$first_chat_id" --limit 3
else
  append_note "No chat ID was available, so message commands were skipped."
fi

if [[ -n "$small_chat_id" ]]; then
  run_step "messages-text-limit-small-chat" env HOME="$synthetic_home" trawl imessage messages --chat "$small_chat_id" --limit "$small_chat_count"
  run_step "messages-json-limit-small-chat" env HOME="$synthetic_home" trawl --json imessage messages --chat "$small_chat_id" --limit "$small_chat_count"
else
  append_note "No chat with 1..$max_all_messages messages was available, so messages --limit was skipped."
fi

if [[ -n "$search_query" ]]; then
  run_step "search-text-limit-three" env HOME="$synthetic_home" trawl imessage search --limit 3 "$search_query"
  run_step "search-json-limit-three" env HOME="$synthetic_home" trawl --json imessage search --limit 3 "$search_query"
  search_json="$last_stdout"
  first_search_ref=$(jq -r '.results[0].ref // empty' "$search_json" 2>/dev/null || true)
  if [[ -n "$first_search_ref" ]]; then
    run_step "open-text-first-search-result" env HOME="$synthetic_home" trawl imessage open "$first_search_ref"
    run_step "open-json-first-search-result" env HOME="$synthetic_home" trawl --json imessage open "$first_search_ref"
  else
    append_note "Search returned no ref, so open was skipped."
  fi
else
  run_step "search-text-empty-hit-shape" env HOME="$synthetic_home" trawl imessage search --limit 3 "imsgcrawl-agent-smoke-no-match"
  run_step "search-json-empty-hit-shape" env HOME="$synthetic_home" trawl --json imessage search --limit 3 "imsgcrawl-agent-smoke-no-match"
  append_note "No --query was supplied, so hit-search quality was not tested."
fi

run_step "contacts-export-text" env HOME="$synthetic_home" trawl imessage contacts export
run_step "contacts-export-json" env HOME="$synthetic_home" trawl --json imessage contacts export

cat >>"$review" <<'EOF'

================================================================================
AGENT REVIEW CHECKLIST
================================================================================

Read the review previews, then open exact raw files for any command where shape,
IDs, completeness, privacy, or size needs full inspection.

- Can an agent discover the useful commands from help alone?
- Does every documented command actually run?
- Do `--help`, `help COMMAND`, and command-local help agree?
- Do text and JSON modes differ intentionally, or is non-JSON secretly JSON?
- Are default limits obvious from help and visible from output shape?
- Does any default output look complete while hiding rows?
- Does text output show counts, completeness, and follow-up commands?
- Does JSON output keep a stable small envelope instead of a giant unstructured blob?
- Can IDs from `chats` be passed directly to `messages`?
- Can refs from `search --json` be passed directly to `open`?
- Are message/search/open/contact outputs human-readable enough for an agent to use?
- Are there machine-only fields, unstable IDs, hashes, or local internals that should not be agent-facing?
- Are errors useful, or only Go/SQLite/parser noise?
- Which outputs should become crawlkit-standard textproto or agent-friendly text later?
EOF

echo "review: $review"
if [[ "$inline_raw" -eq 1 ]]; then
  echo "transcript: $transcript"
fi
echo "raw: $raw_dir"
echo "commands: $commands_tsv"
echo "manifest: $manifest_jsonl"
echo "archive: $archive"

if [[ "$failures" -ne 0 ]]; then
  echo "failed commands: $failures" >&2
  exit 1
fi
