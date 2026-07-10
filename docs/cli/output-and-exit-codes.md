# Output and Exit Codes

- Status: Initial contract

Human output is concise and may use terminal formatting when attached to a TTY. `--json` writes one versioned JSON value to stdout; progress and diagnostics go to stderr. Error strings are explanatory but automation keys off stable codes.

| Exit | Meaning |
|---:|---|
| 0 | requested operation completed |
| 2 | arguments or configuration invalid |
| 3 | prerequisite unavailable or unsupported |
| 4 | permission, consent, or policy denied |
| 5 | stale revision or state conflict |
| 6 | verification failed |
| 7 | local storage or recovery failed |
| 8 | worker or protocol failed |
| 10 | unexpected internal failure |

The implementation may refine codes only through a versioned contract update. Partial results must be marked explicitly and never use exit 0 when the requested operation did not complete.
