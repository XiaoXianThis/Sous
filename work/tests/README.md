# tests

Tests across apps. An app's own unit and integration tests stay inside the app, following its language's conventions.

| directory | purpose |
|---|---|
| `e2e/` | complete flows from the user's point of view, usually with several apps running |
| `integration/` | two or more apps interacting correctly over the contracts in `docs/api/` |
| `fixtures/` | test data shared by the two above |

## Rules

- When a test framework needs its own project files (such as a `package.json` for Playwright), put them in `e2e/` or `integration/` as an unpublished helper project and run its commands there.
- Document in that directory's README how to run the tests and which apps must be running first.
- No real user data or secrets in fixtures.
