{ pkgs, ... }:

{
  cachix.enable = false;

  languages.go = {
    enable = true;
    package = pkgs.go_1_26;
    delve.enable = false;
    lsp.enable = false;
  };

  packages = [
    pkgs.awscli2       # scripts/deploy-website
    pkgs.buf
    pkgs.golangci-lint
    pkgs.protoc-gen-go
    pkgs.openssl
    pkgs.sqlite
    pkgs.swiftlint
    pkgs.jq
  ];

  enterShell = ''
    export PATH="$DEVENV_ROOT/.dev/bin:$PATH"
    # Share Go module cache across worktrees instead of duplicating it in .devenv/state/go.
    export GOPATH="$HOME/go"
    export GOMODCACHE="$HOME/go/pkg/mod"
    # trawlkit/store uses C SQLite (mattn/go-sqlite3); FTS5 is a build tag.
    export GOFLAGS="-tags=sqlite_fts5"
  '';
}
