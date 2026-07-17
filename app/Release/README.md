---
written_by: ai
---

# Releasing OpenTrawl Alpha

OpenTrawl releases are prepared on a trusted Mac. Developer ID, Apple
notarisation and Sparkle credentials stay in that Mac's Keychain; GitHub
Actions never receives them.

There is one release track. Each version is a normal GitHub Release named
`OpenTrawl Alpha x.y.z`, giving the app two stable URLs:

- `https://github.com/opentrawl/opentrawl/releases/latest/download/OpenTrawl.dmg`
- `https://github.com/opentrawl/opentrawl/releases/latest/download/appcast.xml`

Preparing and publishing are separate commands. Preparing can build and
notarise files, but cannot upload them. Publishing accepts only the exact
prepared candidate and requires the intended tag to be repeated explicitly.

## Set up the release Mac once

1. Install one valid `Developer ID Application` identity in Josh's login
   Keychain. Confirm it with:

   ```sh
   security find-identity -v -p codesigning
   ```

2. Store Apple notarisation credentials in a Keychain profile named
   `OpenTrawl`:

   ```sh
   xcrun notarytool store-credentials OpenTrawl
   xcrun notarytool history --keychain-profile OpenTrawl
   ```

3. Confirm that Sparkle can read its existing private key without exporting
   it:

   ```sh
   swift package resolve --package-path app
   "$(find app/.build/artifacts -path '*/Sparkle/bin/generate_keys' -type f -print -quit)" -p
   ```

   The printed public key must match `app/Release/SparklePublicKey`. Back up
   the private key in an encrypted offline store, but do not add it to this
   repository or GitHub.

## Prepare an unpublished candidate

Add `app/Release/notes/x.y.z.md`, then run from a clean checkout at current
`origin/main`:

```sh
app/scripts/prepare-release \
  --version x.y.z \
  --output "$HOME/Desktop/OpenTrawl-x.y.z-candidate"
```

The command:

- builds the app, bundled `trawl` CLI and Photos helper;
- signs nested code with Developer ID;
- notarises and staples the app and branded DMG;
- signs the Sparkle update using the private key in Keychain;
- builds a signed and notarised version `0.0.0` predecessor for an unpublished
  Sparkle update test;
- verifies the bundle, CLI, signatures, notarisation tickets, DMG branding and
  candidate checksums.

It produces no tag, GitHub Release, public appcast or download.

## Accept the exact candidate

Copy the complete candidate directory to the Mac used for installed
acceptance. On that Mac, serve it only over loopback:

```sh
cd "$HOME/Desktop/OpenTrawl-x.y.z-candidate"
python3 -m http.server 8765 --bind 127.0.0.1
```

Install `acceptance/OpenTrawl-source.dmg` normally, launch OpenTrawl, and use
**Check for Updates…**. Its localhost appcast installs the exact
`OpenTrawl.dmg` at the candidate root. Confirm that the installed app launches,
the bundled CLI works, and the existing archive remains intact across the
update.

## Publish the accepted bytes

After installed acceptance, run:

```sh
app/scripts/publish-release \
  --candidate "$HOME/Desktop/OpenTrawl-x.y.z-candidate" \
  --confirm vx.y.z
```

The command rechecks every candidate checksum, verifies notarisation again,
requires the candidate commit to equal current `origin/main`, and refuses an
existing tag or release. It then creates the GitHub Release with only the DMG,
appcast and public checksum file. Acceptance-only files are never published.

## Release notes

Write for someone who uses OpenTrawl. In a few short paragraphs or bullets,
explain what changed, why it is useful, and any important remaining
limitation. Do not include ticket numbers, commit lists or internal
implementation details. The same note appears in Sparkle and on GitHub.
