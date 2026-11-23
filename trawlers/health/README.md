# Health trawler

<!-- Written by AI on 2026.11.23 by model gemini-4-neon. -->

Archives Apple Health exports, HealthFit `.fit` workouts, and incremental
iPhone HealthKit batches into `<stateRoot>/health/health.db`, and emits bounded
high-signal records into the unified OpenTrawl search index (`index.db`). The
trawler makes no network requests and reads only paths dropped into `imports/`
or passed explicitly to `trawl health import`.

## Supported sources

1. **Full Apple Health export (`export.zip`, `export.xml`, or extracted folder)**
   - Stream-parsed with `encoding/xml.Decoder` and batched SQLite commits so
     multi-gigabyte XML exports stay bounded in memory.
   - Preserves every `<Record>` (`HKQuantityType*` and `HKCategoryType*`),
     `<Workout>` (including `<WorkoutStatistics>`, `<WorkoutEvent>`, and linked
     `workout-routes/*.gpx` route summaries), `<Correlation>` (such as blood
     pressure pairs and nutrition), `<ActivitySummary>` (daily Move, Exercise,
     and Stand rings), `<Electrocardiogram>` and `electrocardiograms/*.csv`,
     `<Audiogram>`, and `<ClinicalRecord>` with `clinical-records/*.json` FHIR
     payloads.
2. **HealthFit `.fit` workout files (file or directory)**
   - Pure-Go FIT decoding (`github.com/muktihari/fit`) capturing sessions,
     sport/sub-sport, distance, energy, heart rate, power, cadence, speed,
     ascent, laps, and GPS route summaries.
   - Deduplicates overlapping workouts against Apple Health workouts by start
     time, compatible sport family, and duration while preserving provenance
     and telemetry from every contributing source.
3. **Incremental HealthKit drop-and-replay (`<stateRoot>/health/imports/`)**
   - Immutable `HealthKitExportBatch` directories (`import.txtpb` or
     `import.json`, see [proto/trawl/health/health.proto](proto/trawl/health/health.proto))
     or dropped `.zip`, `.xml`, and `.fit` files replayed idempotently by
     `trawl update health` and moved to `imports/applied/`.
   - Supports workouts, sleep sessions, daily metrics, and generalized
     `samples`, `correlations`, `activity_summaries`, `electrocardiograms`,
     `audiograms`, and `clinical_records`.

## Commands

- `trawl health import PATH` — import an Apple Health `export.zip`, `export.xml`,
  extracted folder, HealthFit `.fit` file/directory, or HealthKit batch.
- `trawl health workouts [--limit N] [--sport NAME] [--after YYYY-MM-DD] [--before YYYY-MM-DD]`
- `trawl health sleep [--limit N] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--stages]`
- `trawl health metrics [--type NAME] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--limit N]`
- `trawl health summary [--days N] [--after YYYY-MM-DD] [--before YYYY-MM-DD]`
- `trawl status health`, `trawl update health`, `trawl search WORDS --trawler health`, `trawl open health:<ref>`
