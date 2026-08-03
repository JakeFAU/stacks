## 2026-08-03 - Temporal snapshot caller context precedence

**Learning:** PostgreSQL temporal snapshot errors feed both caller classification and snapshot outcome telemetry, so an already-failed caller context must take precedence when the returned database error reports a different cancellation type.

**Action:** In `temporalSnapshotError`, classify `ctx.Err()` before the returned error while preserving the bounded operation message and established conflict/not-found sentinels.
