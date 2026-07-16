---
written_by: ai
---

# Releasing OpenTrawl Alpha

OpenTrawl has one release track. Each version is a normal GitHub Release named
`OpenTrawl Alpha x.y.z`; it is not marked as a GitHub prerelease. This keeps the
latest download and the Sparkle feed at stable URLs:

- `https://github.com/opentrawl/opentrawl/releases/latest/download/OpenTrawl.dmg`
- `https://github.com/opentrawl/opentrawl/releases/latest/download/appcast.xml`

The release workflow is manual. It builds an arm64 app, includes the complete
`trawl` CLI, signs the nested code from the inside out, notarises and staples
the app and disk image, creates the signed Sparkle appcast, verifies the final
disk image, and then creates the GitHub Release.

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

## Required GitHub environment and secrets

Configure a protected `release` environment with these secrets:

- `DEVELOPER_ID_APPLICATION_P12`: base64-encoded Developer ID Application
  certificate and private key;
- `DEVELOPER_ID_APPLICATION_P12_PASSWORD`;
- `APP_STORE_CONNECT_API_KEY`: the App Store Connect API private key text;
- `APP_STORE_CONNECT_API_KEY_ID`;
- `APP_STORE_CONNECT_API_ISSUER_ID`;
- `SPARKLE_ED_PRIVATE_KEY`: the private key file exported by Sparkle;
- `SPARKLE_ED_PUBLIC_KEY`: the matching public key.

The workflow is the only publishing path. Local scripts can build and verify
an ad hoc-signed artifact without production credentials, but they do not
publish, upload or alter a release channel.
