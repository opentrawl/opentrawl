{ pkgs, ... }:

{
  cachix.enable = false;

  packages = [
    pkgs.go
    pkgs.sqlite
  ];

  scripts.verify.exec = ''
    GOWORK=off go mod tidy
    GOWORK=off go test ./...
  '';
}
