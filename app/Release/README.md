---
written_by: ai
---

# Releasing OpenTrawl Alpha

OpenTrawl releases are prepared on a trusted Mac. Developer ID, Apple
notarisation and Sparkle credentials stay on that Mac; GitHub Actions never
receives them.

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

2. In App Store Connect, create a team API key with the Developer role. An
   individual API key cannot use `notarytool`. Download the `.p8` file once,
   then install and validate it locally:

   ```sh
   app/scripts/setup-release-credentials \
     --notary-key "$HOME/Downloads/AuthKey_KEYID.p8" \
     --notary-key-id KEYID \
     --notary-issuer ISSUER_UUID
   ```

   The script moves the one-time download to
   `~/.config/opentrawl/release/app-store-connect-api-key.p8`, stores its public
   identifiers beside it and validates the credential against Apple's notary
   service. Apple Account passwords and notarisation Keychain profiles are not
   used.

3. Restore the Sparkle private key to
   `~/.config/opentrawl/release/sparkle-ed25519-private-key`. Before the first
   public release only, create the initial key with:

   ```sh
   app/scripts/setup-release-credentials --create-sparkle-key
   ```

   The command refuses a key that does not match `app/Release/SparklePublicKey`.
   Back up the private file in an encrypted offline store. Never add it to this
   repository or GitHub. Once any public version ships, losing this key means
   existing installations cannot trust a replacement update key.

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
- signs the Sparkle update using the explicit local private key file;
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
