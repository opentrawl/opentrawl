---
written_by: ai
---

# Telegram

The Telegram crawler imports Telegram cloud history or Telegram for macOS
Postbox data into a searchable SQLite archive.

## Connect directly to Telegram (optional, zero Full Disk Access)

Direct sign-in uses Telegram's official MTProto API. It needs neither the
desktop app nor Full Disk Access.

Run `trawl telegram connect --phone` with your number in international format.
Run it again with the same number and `--code` after Telegram sends a code. Add
`--password` if your account uses two-step verification. The command saves
Telegram's pending code hash locally between steps.

For QR sign-in, run `trawl telegram connect --qr`, then run
`trawl telegram login-qr` in another terminal. Open the indicated image and
scan it from Devices in Telegram. Keep the connect command running while you
approve the sign-in.

The signed-in session is stored as `mtproto-session.json` beside the archive,
with permissions limited to your user. `trawl update telegram` uses it to
download cloud dialogs, messages and available media metadata. Use
`trawl telegram disconnect` to revoke the session while keeping the archive.

Set `api_id` and `api_hash` in Telegram's `config.toml` to use your own
Telegram application credentials. Standalone Go callers can also set
`OPENTRAWL_TELEGRAM_API_ID` and `OPENTRAWL_TELEGRAM_API_HASH`.

## Source and storage

Update uses the native Postbox store when there is no direct session:

```text
~/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram
```

The archive is `~/.opentrawl/telegram/telegram.db`; archived media is under
`~/.opentrawl/telegram/media/`. A normal update starts with the messages and media
cached by Telegram on this Mac.

Run `trawl update telegram --full-history` to download older messages into
OpenTrawl through the existing Telegram session. The download is resumable.
After it completes, normal updates keep the downloaded message archive current.
Attachments are separate: add `--fetch-media` to download missing files for
messages already in the archive. Neither option launches Telegram or starts a
login flow.

Use `trawl update telegram --path /path/to/copied/source` to import a copied
Postbox source explicitly.

## Commands

```sh
trawl update telegram
trawl update telegram --full-history
trawl update telegram --fetch-media
trawl telegram conversations --limit 20
trawl telegram messages --conversation LINK --after DATE
trawl search "words" --trawler telegram
trawl open LINK
```

The CLI uses normal text output. The archive preserves available folders,
topics, replies, pins, edits, forwards, reactions and media metadata as
source-native Telegram facts; it does not turn them into a cross-source schema.

## Privacy

Message text, chat and sender names, phone numbers, usernames, media metadata
and local paths remain private. Search and browse commands do not upload them.
A direct update contacts Telegram. Without a direct session, a normal update
reads local Postbox until full history is enabled.
Full-history updates and later updates contact Telegram to keep that archive
current. An update with `--fetch-media` separately requests missing attachments.
