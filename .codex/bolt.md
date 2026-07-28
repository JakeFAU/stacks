## 2026-07-28 - Evidence spans rehydrate immutable documents

**Learning:** Loading one evidence span reconstructs its canonical document
through separate version, section, and source-revision queries. Relationship
reads multiply those three queries when several evidence links share one
immutable document version.

**Action:** Reuse a fully validated `DocumentVersionRecord` within one coherent
relationship read, and evaluate this path with the `5E` versus `2E + 3D`
evidence-query model, where `E` is evidence links and `D` is distinct document
versions.
