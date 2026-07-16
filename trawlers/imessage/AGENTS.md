---
written_by: ai
---

# AGENTS.md

`imsgcrawl` owns read-only access to Apple Messages, its source-native archive,
its source commands and its small internal People snapshot contract.

- Keep Messages database parsing and source meaning inside this crawler. Put
  only provider-neutral mechanics used by more than one source in `trawlkit`.
- Do not add cross-source identity, people graphs, clustering or derived life
  artefacts here.
- Follow the root control contract. Keep the People snapshot limited to
  `display_name` and `phone_numbers` until all consumers change together.
- Tests use synthetic temporary SQLite fixtures. Never read the live Messages
  database or publish messages, contacts, attachments, account identifiers or
  identifying local paths.
