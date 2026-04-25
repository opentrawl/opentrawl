# Changelog

All notable changes to this project are documented here.

The format follows Keep a Changelog, and this project uses Semantic Versioning.

## [Unreleased]

### Added

- Initial read-only WhatsApp Desktop archive CLI.
- macOS source discovery for
  `~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared`.
- Safe SQLite snapshot import for `ChatStorage.sqlite` and `ContactsV2.sqlite`.
- Archive schema for chats, contacts, groups, group participants, messages, and
  FTS5 search.
- Commands: `doctor`, `import`, `status`, `chats`, `messages`, and `search`.
- JSON output mode for scripting.
- Message filters for chat, sender, date range, direction, media presence, sort
  order, and limit.
- WhatsApp CoreData extraction for `ZWACHATSESSION`, `ZWAMESSAGE`,
  `ZWAMEDIAITEM`, `ZWAGROUPINFO`, and `ZWAGROUPMEMBER`.
- Apple-epoch timestamp conversion.
- Group sender resolution through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media metadata extraction through both message-to-media join paths.
- Build, lint, coverage, and test automation through `make check`.

### Changed

- Project now targets Go 1.26.
- Dependencies updated, including `modernc.org/sqlite` v1.50.0.
- Linting tightened with `golangci-lint` v2 configuration.

### Security

- Import is read-only against WhatsApp's app container.
- WhatsApp SQLite files are copied to a temporary snapshot before extraction.
- Archive writes are isolated to the configured `wacrawl` database.

### Quality

- Coverage gate added at 85% total statement coverage.
- Current test coverage: 86.2%.
- Focused tests cover CLI behavior, archive storage, import fixtures, filtering,
  search, JSON output, schema edge cases, and failure paths.
