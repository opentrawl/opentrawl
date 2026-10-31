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
   devenv shell -- app/scripts/setup-release-credentials \
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
devenv shell -- app/scripts/prepare-release \
  --version x.y.z \
  --output "$HOME/Desktop/OpenTrawl-x.y.z-candidate"
```

The command:

- builds the app and bundled `trawl` CLI;
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
devenv shell -- app/scripts/publish-release \
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

## Installed lifecycle

<!-- Written by AI on 2026.10.31 by model gpt-6.1-sol. -->

The Mac app owns software updates through Sparkle. The bundled `trawl` CLI
owns archive commands; invoking it does not start the software updater. Keep
`~/.local/bin/trawl` linked to `/Applications/OpenTrawl.app/Contents/Helpers/trawl`.
Never replace that link with a development binary.

On an archive host, after installing an accepted release, run
`app/scripts/enable-background-updates` while the app is stopped.
It enables automatic checks and downloads and installs a per-user login agent.
A release-only Mac does not run the app at login or host archives. Do not run
this setup script there or create a local CLI link. Agents on that Mac use the
archive host's CLI over SSH. After publishing each accepted release, install
its app on the release Mac while stopped, and enable automatic checks and
installs in its preferences. It then checks whenever opened. The app's `--background` launch opens no window.
Onboarding and Full Disk Access must already be complete for archive refreshes.
The app refreshes archives independently of its windows, using each source's
normal refresh limits. Never bypass source-specific request limits to test an
app install.

Release builds check the production appcast on launch and hourly thereafter.
Sparkle verifies the signed download. Once downloaded, the app waits until no
`trawl` process for the current user and no app archive refresh is running, then
invokes Sparkle's immediate installation handler. The app relaunches with no
window. An agent does not need permission for routine installation, update,
health checks or a release already authorised within its task.

Before replacing an installed app, preserve its archive and the existing app
outside this public repository. Do not kill CLI commands to make installation
proceed. Do not edit a signed bundle's Info.plist. Versions through 0.1.4 need
one last manual Sparkle installation because they forbid automatic downloads.

Installed acceptance must exercise Sparkle against the exact candidate bytes,
then inspect the running version, bundled CLI and existing archive. Automatic
updates additionally require an unattended loopback-feed hop: start the real
app with automatic downloads enabled, make a signed newer candidate available,
and observe replacement and relaunch without clicking an update control. Hold
a real CLI command open during this test and verify installation waits for it.
Keep test feeds and downloaded candidates private, clear the feed override,
and restore the accepted production release before completion. Publish only
a candidate that passed installed acceptance.

To check an archive host, compare its installed CFBundleShortVersionString and
CFBundleVersion with the production appcast, run the bundled CLI's `--version`
and `status`, confirm the app process and login agent, and read effective
`SUEnableAutomaticChecks` and `SUAutomaticallyUpdate` preferences. Check there
is no `SUFeedURL` override left from acceptance. Software update events use the
`org.opentrawl.trawl` logging subsystem and `SoftwareUpdates` category;
Sparkle also logs its download, verification and installation failures. A
preference read alone does not prove an update works.
