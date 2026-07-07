# OpenTrawl brand assets

The mark: a red faceted diamond drawn as a network — nodes and edges form the
gem, a faint context graph wraps the frame. The network trawls; the diamond is
the catch.

Palette (matches the website): red #e63323, black #101010, white, greys.
No gold, no other colours in the mark.

## Layout

- `src/` — canonical vector sources. `mark.svg` is the full mark, `mark-small.svg`
  the simplified variant used below 128px (thicker seams, vertex nodes only,
  no wrap), `glyph-menubar.svg` the mono menu bar glyph, `lockup.svg` the mark
  plus wordmark. Two colour tokens are replaced at build time: `#FNT` (faint
  wrap: `#c9c9c9` on light, `#48484a` on dark) and `#INK` (text/glyph ink).
  The gem's own seams are always literal `#101010`, never theme-swapped.
- `OpenTrawl.icon/` — hand-authored Icon Composer bundle (icon.json + layers).
  The system renders Liquid Glass from it; the artwork carries no baked
  effects. `fill-specializations` pins the light background for dark mode:
  the icon does not change colour in dark mode.
- `build/` — python scripts that regenerate `exports/` from `src/` (headless
  Chrome for rendering, `iconutil` for the icns). Run from inside `build/`.
- `exports/` — committed build outputs: legacy `OpenTrawl.icns` (light tile,
  full ladder), (compile `Assets.car` for macOS 26+ from `OpenTrawl.icon` — see below; it is not committed because it is a >1MB reproducible binary), menu bar template PNGs
  (16/18pt + @2x, black + alpha only), and the web set (favicons, touch icon,
  mark, lockups).

## The Apple pipeline (no GUI needed)

Compile the .icon bundle:

    DEVELOPER_DIR=/Applications/Xcode-beta.app xcrun actool \
      --output-format human-readable-text --notices --warnings \
      --platform macosx --minimum-deployment-target 15.0 \
      --app-icon OpenTrawl --output-partial-info-plist partial.plist \
      --compile out OpenTrawl.icon

Preview any appearance (Default, Dark, TintedLight, TintedDark, ClearLight,
ClearDark):

    "/Applications/Xcode-beta.app/Contents/Applications/Icon Composer.app/Contents/Executables/ictool" \
      OpenTrawl.icon --export-image --output-file out.png \
      --platform macOS --rendition Default --width 512 --height 512 --scale 1

Known caveat: ictool previews ignore `fill-specializations`, so its Dark
export shows an auto-darkened tile. The real system renderer reads the pin
from the compiled Assets.car. Verify in a live dock once the Mac app ships
the icon.

Ship in an app bundle: `Assets.car` into `Contents/Resources`, set
`CFBundleIconName` to `OpenTrawl`, keep `OpenTrawl.icns` alongside with
`CFBundleIconFile` for pre-Tahoe macOS, sign last.

Menu bar: load a template PNG pair (or the glyph SVG) with
`NSImage.isTemplate = true`; artwork is black + alpha only and the system
tints it per appearance.
