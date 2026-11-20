---
written_by: ai (claude opus 5.5, 2026.11.20)
---

# Gmail

The Gmail trawler archives one Gmail mailbox through the Gmail REST API with
the `gmail.readonly` scope. The app on this Mac talks directly to Google;
there is no OpenTrawl server.

## Commands

```sh
trawl gmail connect      # sign in to Google in the browser
trawl update gmail
trawl status gmail
trawl search "words" --trawler gmail
trawl open LINK
trawl gmail disconnect   # revoke the grant; the archive stays
```

## Connection

`connect` runs a loopback OAuth flow (PKCE S256, `127.0.0.1`, random port)
in the browser. The refresh token is stored in the macOS Data Protection
Keychain (service `com.opentrawl.gmail`, account `oauth-refresh-token`) in
the signed app's default access group. Every token request carries a DPoP
proof signed by a Secure Enclave P-256 key (`com.opentrawl.gmail.dpop-key`),
so the refresh token cannot be used from another Mac. Access tokens stay in
memory. Ad hoc and unsigned builds cannot use the Data Protection Keychain.

The release build gets the Google desktop client secret through
`app/scripts/build-release --google-oauth-client-secret FILE`, which sets
`gmail.DefaultGoogleOAuthClientSecret` with `-ldflags`. A build without it
cannot connect.

An archive is bound to the first Google account that connects. Another
account is refused and its grant revoked. `disconnect` revokes the grant at
Google and removes the Keychain item and the Secure Enclave key.

## Update

- The first update lists every message outside Spam and Trash, then fetches
  each one in raw form. Progress is recorded, so an interrupted listing
  resumes without fetching a message twice.
- Later updates read Gmail's history since the stored cursor: new messages
  are fetched raw, label changes fetch only labels, and deleted messages or
  messages moved to Spam or Trash are marked deleted from Gmail. Rows are
  never removed.
- When Google no longer keeps the history (HTTP 404), the mailbox is listed
  again. Messages already archived are not fetched again; their labels come
  from Gmail's per-label listings.
- Requests are paced at one every 210 ms with five concurrent fetches, and
  retried on rate limits and server errors.

## Storage

- `~/.opentrawl/gmail/gmail.db`: messages, full RFC 5322 headers, labels,
  attachment names, types and sizes, and the history cursor.
- `~/.opentrawl/gmail/originals/`: each raw message, written once as
  `<sha256>.eml.zst`. When the shared parser in `trawlkit/mailparse`
  changes, messages are parsed again from these files, not from Gmail.

Attachment contents are not downloaded.
