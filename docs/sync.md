---
written_by: ai
---

# Syncing with upstreams

Imported crawler modules are git subtrees of upstream repos. They live
under `trawlers/` with surface-name directory names. The full history
was imported at creation, so upstream attribution is preserved.

Three commands cover everything:

- `scripts/sync list` shows every subtree directory, its upstream, and where
  outbound changes go.
- `scripts/sync pull <dir>` merges upstream main into the directory.
  Run it before starting work in a subtree.
- `scripts/sync push <dir>` extracts the directory's commits onto a
  `trawl-sync-<dir>` branch on the outbound repo and prints the exact
  `gh pr create` command to open the PR.

Rules:

- keep a change either monorepo-only or subtree-only per commit; a
  commit that spans a subtree and other directories cannot be pushed
  upstream cleanly
- pull before push, and resolve conflicts in the monorepo
- upstream PRs carry our commits verbatim; write commit messages that
  make sense to upstream reviewers on their own
