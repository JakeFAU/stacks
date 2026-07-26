## 2026-07-26 - Loopback OAuth callback ownership

**Risk Pattern:** The one-shot Google OAuth flow can be reached by unrelated loopback requests before the provider redirects the operator.

**Learning:** Rejecting a mismatched state is not sufficient if that unauthenticated request also consumes the callback result and terminates authorization.

**Prevention:** Only state-authenticated callbacks may complete or terminate the flow; reject unknown-state requests without publishing a result.
