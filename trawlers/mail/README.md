---
written_by: ai
---

# Mail

The Mail trawler archives the messages Apple Mail keeps on this Mac: iCloud
Mail and every other Mail.app account. It needs Full Disk Access and no
passwords. It makes no network requests.

## What it reads

- `~/Library/Mail/V<N>/MailData/Envelope Index`, the highest `V<N>`, for the
  mailboxes, the messages, read state and received time.
- `~/Library/Accounts/Accounts4.sqlite` for account names.
- The `.emlx` and `.partial.emlx` message files in each account folder. A
  partial file holds the headers and text; Apple Mail keeps its attachments
  in a separate `Attachments` folder, from which the trawler takes their
  sizes.

Both databases are opened read-only without lock files or shared-memory
writes, so Mail.app is never blocked or changed.

It skips Junk and Trash folders by name, and skips a Gmail account when the
Gmail trawler is bound to the same address. An account counts as Gmail only
by its Google account type or its `[Gmail]` or `[Google Mail]` folders: an
iCloud account's Apple ID can be a Gmail address.

## Storage

- `~/.opentrawl/mail/mail.db`: the searchable archive.
- `~/.opentrawl/mail/originals/`: each raw message once, compressed with
  zstd and named by its SHA-256. A stored file is never rewritten.

An update reads only message files that are new or changed. When the parser
changes, it parses the stored originals again instead of Apple Mail's
message files, including messages that have left Apple Mail. The originals
alone cannot rebuild `mail.db`: the mailbox, account, read state and time
come from Apple Mail and live only in the archive.
A message that leaves Apple Mail stays in the archive, marked with the time
it left; it no longer appears in search but still opens by its link.

## Commands

```sh
trawl update mail
trawl search "words" --trawler mail
trawl open LINK
```
