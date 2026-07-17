---
written_by: ai
---

# Releasing OpenTrawl Alpha

OpenTrawl has one release track. Each version is a normal GitHub Release named
`OpenTrawl Alpha x.y.z`; it is not marked as a GitHub prerelease. This keeps the
latest download and the Sparkle feed at stable URLs:

- `https://github.com/opentrawl/opentrawl/releases/latest/download/OpenTrawl.dmg`
- `https://github.com/opentrawl/opentrawl/releases/latest/download/appcast.xml`

The release workflow is manual. It first builds an unpublished candidate: an
arm64 app containing the complete `trawl` CLI, with nested code signed from
the inside out, the app and disk image notarised and stapled, and a signed
Sparkle appcast. The exact candidate is retained as a GitHub Actions artifact
for installed acceptance. Candidate creation does not create a tag, GitHub
Release, public appcast or stable download URL.

The artifact also contains `acceptance/OpenTrawl-source.dmg` and
`acceptance/appcast.xml`. The source is a signed and notarised version `0.0.0`
build whose feed points to localhost. Serve the artifact directory on
`127.0.0.1:8765`, install the source DMG, and use the app's normal update
command. Its signed acceptance appcast targets the exact production candidate
DMG at the artifact root. Neither acceptance file is uploaded to the GitHub
Release.

Publication is a separate job protected by the `release-publish` environment.
Approve it only after installing and accepting that exact Actions artifact.
The job downloads and verifies the same bytes again before creating the GitHub
Release. Rejecting or leaving the deployment pending does not create a tag,
release, appcast or public download.

## Release notes

Before releasing `x.y.z`, add `app/Release/notes/x.y.z.md`. Write for someone
who uses OpenTrawl, not for the people who built it. In a few short paragraphs
or bullets, explain:

1. what changed;
2. why that change is useful;
3. any important limitation that remains.

Keep ticket numbers, commit lists and internal implementation detail out. The
same note appears in Sparkle and on GitHub, so there is one human explanation
of the release.

## Required GitHub environments and secrets

Configure `release-signing` with these secrets:

- `DEVELOPER_ID_APPLICATION_P12`: base64-encoded Developer ID Application
  certificate and private key;
- `DEVELOPER_ID_APPLICATION_P12_PASSWORD`;
- `APP_STORE_CONNECT_API_KEY`: the App Store Connect API private key text;
- `APP_STORE_CONNECT_API_KEY_ID`;
- `APP_STORE_CONNECT_API_ISSUER_ID`;
- `SPARKLE_ED_PRIVATE_KEY`: the private key file exported by Sparkle;
- `SPARKLE_ED_PUBLIC_KEY`: the matching public key.

Configure `release-publish` with Josh as a required reviewer. It has no
secrets. Its approval is the publication decision after installed acceptance,
not an approval to begin signing.

The workflow is the only publishing path. Local scripts can build and verify
an ad hoc-signed artifact without production credentials, but they do not
publish, upload or alter a release channel.
