## 2026-07-27 - Bounded loopback OAuth headers

**Learning:** The shared Google OAuth callback listener is loopback-only but still accepts unauthenticated local TCP connections while authorization is pending.

**Action:** Keep a nonzero header-read timeout on that callback server so incomplete local requests cannot retain connection resources for the full authorization lifetime.
