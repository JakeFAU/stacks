## 2026-07-27 - Bounded loopback OAuth headers

**Learning:** The shared Google OAuth callback listener is loopback-only but still accepts unauthenticated local TCP connections while authorization is pending.

**Action:** Keep a nonzero header-read timeout on that callback server so incomplete local requests cannot retain connection resources for the full authorization lifetime.

## 2026-07-31 - Control-free planner predicates

**Learning:** Temporal query planner proposals supply untrusted predicate text that is later rendered in private terminal output, while the provider-free predicate contract intentionally preserves nonblank bytes exactly.

**Action:** Reject Unicode control characters at the planner proposal boundary before composing a deterministic query request; do not weaken the exact-byte predicate contract for typed queries.
