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
    pkgs.golangci-lint
    pkgs.sqlite
    pkgs.jq
  ];

  enterShell = ''
    export PATH="$DEVENV_ROOT/.dev/bin:$PATH"
    # crawlkit/store uses C SQLite (mattn/go-sqlite3); FTS5 is a build tag.
    export GOFLAGS="-tags=sqlite_fts5"
  '';
}
