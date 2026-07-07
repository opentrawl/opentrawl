# Verified toolchain recipes (smoke-tested 2026-07-07, arm64 macOS)

Everything below ran green end-to-end under devenv. Reuse verbatim;
don't rediscover.

## Nix pinning

Root `devenv.nix` additions (house ruling: demo/ uses the ROOT
devenv per vision.md — module-local devenvs are only for
subtree-synced standalone repos):

```nix
packages = [
  pkgs.nodejs        # 24 LTS — Remotion needs nothing more than node+npm
  pkgs.ffmpeg-full   # 8.x; libx264, aac, drawtext(freetype) confirmed
  pkgs.vhs           # 0.11; self-contained, no ttyd/browser to manage
  pkgs.jetbrains-mono
  pkgs.montserrat    # Black weight for hooks/captions; NOT pkgs.inter
                     # (variable-font-only, libass can't pick Bold)
];
# enterShell: export DEMO_FONT_DISPLAY/DEMO_FONT_MONO as nix store
# paths — ffmpeg drawtext takes fontfile=<path>, Remotion loads via
# @font-face path. No fontconfig plumbing needed on macOS.
```

## Remotion (versions proven: remotion 4.0.485, react 19, node 24)

- `npm create video` is interactive-only — hand-write package.json +
  src/ (registerRoot, one `<Composition>` per aspect ratio). Better
  anyway: sizes live in code.
- First render auto-downloads chrome-headless-shell (~94MB) into
  `node_modules/.remotion/` — project-local, lockfile-pinned, no
  Gatekeeper issues. Pre-warm with any render; nothing else to pin.
- Cold render ~10s, warm ~3s for 90 frames. Render both:
  `npx remotion render Landscape out/x-16x9.mp4` / `… Vertical …`.

## ffmpeg recipes (exact filter strings proven)

Run each invocation from its own small `.sh` file — filter strings
do NOT survive quoting through `devenv shell -- bash -c "…"`.

- Caption band (put y≥100 to clear the terminal's top line):
  `-vf "drawtext=fontfile=$DEMO_FONT_MONO:text='…':fontcolor=white:fontsize=54:box=1:boxcolor=black@0.6:boxborderw=20:x=(w-text_w)/2:y=100"`
- Vertical 1080x1920 with blurred self-background:
  `-filter_complex "[0:v]split=2[bg][fg];[bg]scale=1080:1920:force_original_aspect_ratio=increase,crop=1080:1920,boxblur=40:5,eq=brightness=-0.05[bgblur];[fg]scale=1080:-2[fgscaled];[bgblur][fgscaled]overlay=(W-w)/2:(H-h)/2,format=yuv420p"`
- Speed-ramp middle segment: trim into 3 parts, middle gets
  `setpts=(PTS-STARTPTS)/2`, concat.
- Music bed: `-map 0:v -map 1:a -filter:a "volume=-18dB" -c:v copy
  -c:a aac -shortest`.
- Frame inspection: `-vf fps=2` → PNGs, then LOOK at them.

## VHS

`vhs demo.tape` just works under devenv (0.11 uses its own PTY
capture; no browser). 1920x1080 h264 out. Only needed for
pure-terminal shots; the Remotion player is the default.

## Gitignore for demo/

`node_modules/`, `out/`, `raw/`, `.remotion/`.
