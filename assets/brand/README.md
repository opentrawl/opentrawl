# OpenTrawl brand assets

The mark: a red faceted diamond drawn as a network — nodes and edges form the
gem, a faint context graph wraps the frame. The network trawls; the diamond is
the catch.

Palette (matches the website): red #e63323, black #101010, white, greys.
No gold, no other colours in the mark.

## Brand guidelines

Which asset where:

- app icon (macOS 26+): `OpenTrawl.icon`, compiled to Assets.car at build time
- app icon (pre-Tahoe): `exports/icns/OpenTrawl.icns`
- menu bar: `exports/menubar/template-*.png` (or `src/glyph-menubar.svg`)
- website: `exports/web/` — favicons, touch icon, `mark.svg`, lockups
- social: `exports/social/` — X banner 1500x500, avatar (400 for upload);
  `avatar-glass.jpg` (1024, white background) is the system-rendered Liquid
  Glass tile for GitHub and anywhere else that wants the "real app icon" look
- disk image: `exports/dmg/background.png` — the branded drag-to-Applications
  Finder background, generated from `src/dmg-background.svg`
- video and print: `src/lockup.svg` (light or dark ink), or `src/mark.svg` alone

Rules, learned the hard way — do not relitigate without a strong reason:

- the gem's seams and node rings are always #101010, on every background;
  never swap them to white on dark
- the icon background does not change in dark mode (light tile pinned)
- below 128px, always use `mark-small.svg`: no wrap, thicker seams, vertex
  nodes only; the full mark's detail turns to mud at small sizes
- the faint wrap never touches the gem's nodes — spokes stop short, the
  outer ring breaks around the extreme vertices
- gem cells use exactly the three reds (#ef5a48, #e63323, #c22417 plus
  #f2634f on the crown corners); nodes stay red — no multicolour nodes
- no gold, no gradients in the artwork, no baked shadows or gloss (the
  system renders glass), no outlines around the gem, no photorealism
- menu bar glyph is black + alpha only; the seam cuts are transparent,
  not white
- avatars for social are opaque white, not transparent (platforms
  recompress and composite unpredictably)
- wordmark: lowercase Helvetica bold, "open" in ink, "trawl" in red

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

Regenerate `exports/social/avatar-glass.jpg` (the glass tile as a flat image):
export the Default rendition with ictool at 1024, composite onto white
(headless Chrome over a plain white page works), convert to JPEG with
`sips -s format jpeg -s formatOptions 92`. The transparent ictool PNG is
not committed (over the repo's 1MB binary limit).

Ship in an app bundle: `Assets.car` into `Contents/Resources`, set
`CFBundleIconName` to `OpenTrawl`, keep `OpenTrawl.icns` alongside with
`CFBundleIconFile` for pre-Tahoe macOS, sign last.

Menu bar: load a template PNG pair (or the glyph SVG) with
`NSImage.isTemplate = true`; artwork is black + alpha only and the system
tints it per appearance.
