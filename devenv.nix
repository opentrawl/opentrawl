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
    pkgs.buf
    pkgs.golangci-lint
    pkgs.protoc-gen-go
    pkgs.sqlite
    pkgs.jq
  ];

  enterShell = ''
    export PATH="$DEVENV_ROOT/.dev/bin:$PATH"
    # trawlkit/store uses C SQLite (mattn/go-sqlite3); FTS5 is a build tag.
    export GOFLAGS="-tags=sqlite_fts5"
    "$DEVENV_ROOT/scripts/dev-bin" || echo "dev-bin: build failed, keeping existing .dev/bin binaries (see errors above)"
  '';
}
