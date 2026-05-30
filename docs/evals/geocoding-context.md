# Geocoding Context For Photo Cards

Raw GPS alone is not enough for good photo cards. The model needs a small,
human-readable place context that is useful without pretending the exact POI is
known.

## Contract

Before prompting, convert coordinates into:

- `area`: coarse trail, such as `Country -> City -> district/area`;
- `specific_candidates`: ranked candidate venues, landmarks, terminals,
  addresses, or POIs, each with source and confidence;
- `nearby_photo_context`: nearby asset hints from the same time/GPS cluster,
  such as a boarding pass, hotel sign, museum entrance, restaurant receipt, or
  transit platform;
- `warnings`: reasons the place may be ambiguous, such as dense urban POIs,
  weak GPS accuracy, indoor capture, or stale metadata.

The prompt should use this context to understand the image, not to overwrite
visual evidence. A nearby boarding pass can make an airport-lounge snack image
understandable. A no-drone sign mentioning an airport control zone should not
move a city temple photo to the airport.

## Precision

Cards should expose both generic and specific context:

```text
Area: Country -> City -> airport/rail hub area
Specific candidate: likely terminal complex, exact terminal unresolved
```

Do not put raw coordinates in prose. Store them as private evidence in the local
archive. Future place resolution can use Apple/CoreLocation, OpenStreetMap, or a
commercial geocoder, but each candidate needs source, distance/accuracy, and a
confidence label.
