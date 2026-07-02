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
  ];

  enterShell = ''
    export PATH="$DEVENV_ROOT/.dev/bin:$PATH"
  '';
}
