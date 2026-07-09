{ pkgs, ... }:

let
  # pkgs.jetbrains-mono in the current nixpkgs is built from source via
  # gftools -> python3.13-afdko, whose pytest suite fails (the same afdko
  # blocker that broke pkgs.montserrat). Use the upstream prebuilt release
  # instead — no afdko, no font compilation, fast. Lays the TTFs out under
  # share/fonts so the DEMO_FONT_MONO find below still works.
  jetbrainsMono = pkgs.runCommandNoCC "jetbrains-mono-2.304" {
    nativeBuildInputs = [ pkgs.unzip ];
    src = pkgs.fetchurl {
      url = "https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip";
      hash = "sha256-b2N2xu0pYOqKljzXOH7J124/YpElvDPR/c1+twEve78=";
    };
  } ''
    mkdir -p $out/share/fonts/truetype
    unzip -j $src 'fonts/ttf/*.ttf' -d $out/share/fonts/truetype
  '';
in
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

    # demo/ video toolchain — the demo builds with the tool, one devenv
    # (see .claude/skills/opentrawl-demo/toolchain.md).
    pkgs.nodejs        # 24 LTS — Remotion agent player
    pkgs.ffmpeg-full   # 8.x; libx264, aac, drawtext(freetype)
    pkgs.asciinema     # record the real terminal session (no browser needed)
    pkgs.asciinema-agg # render the recording straight to gif/frames
    jetbrainsMono      # prebuilt upstream release (see let-binding above)
    # NB: pkgs.montserrat and pkgs.jetbrains-mono are both broken in nixpkgs
    # right now (they build from source via python3.13-afdko, whose test suite
    # fails). Mono uses the prebuilt JetBrains release; the display font for
    # captions/hooks is a prebuilt TTF vendored under demo/assets/fonts.
  ];

  enterShell = ''
    export PATH="$DEVENV_ROOT/.dev/bin:$PATH"
    # trawlkit/store uses C SQLite (mattn/go-sqlite3); FTS5 is a build tag.
    export GOFLAGS="-tags=sqlite_fts5"
    # Font paths for ffmpeg drawtext (fontfile=<path>) and Remotion @font-face.
    # Mono comes from the nix store; the display font is a prebuilt TTF vendored
    # under demo/assets/fonts (pkgs.montserrat is broken in nixpkgs right now).
    export DEMO_FONT_MONO="$(find ${jetbrainsMono}/share/fonts -iname 'JetBrainsMono-Regular*.ttf' 2>/dev/null | head -1)"
    export DEMO_FONT_DISPLAY="$DEVENV_ROOT/demo/assets/fonts/Montserrat-Black.ttf"
    [ -f "$DEMO_FONT_DISPLAY" ] || export DEMO_FONT_DISPLAY="$DEMO_FONT_MONO"
    "$DEVENV_ROOT/scripts/dev-bin" || echo "dev-bin: build failed, keeping existing .dev/bin binaries (see errors above)"
  '';
}
