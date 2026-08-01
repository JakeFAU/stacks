## 2026-08-01 - Google pagination must make progress

**Learning:** Google API continuation tokens remain untrusted provider data; a repeated non-empty token can keep Drive or Directory enumeration from terminating even when every individual response is otherwise valid.

**Action:** Reject repeated Google pagination tokens at the owning adapter without exposing token values, while continuing to accept distinct tokens and natural empty-token completion.
