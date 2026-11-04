# memory

Lessons learned by agents: pitfalls and proven approaches. Skim this before a larger task.

## Rules

- Write an entry only when the same problem comes up a second time, or when the user asks you to remember something.
- Record only what cannot be seen directly in the code and docs. Lessons about one app go to that app's `AGENTS.md`.
- Entry format: `- YYYY-MM-DD Symptom: ... Cause: ... Fix: ...`
- Delete entries that are no longer true. Past 50 entries, move coherent groups into `rules/` or `skills/` (with user approval).

## Entries

- 2026-11-03 Symptom: a coding agent (claude, cursor, codex) reports "not logged in" or hangs in `authenticate` when probed from an agent's shell. Cause: the agents keep their logins in the macOS keychain, which the sandboxed shell cannot read. Fix: probe real agents outside the sandbox; the real daemon runs as the user and is not affected.
- 2026-11-03 Symptom: DeepSeek answers HTTP 503 "Service is too busy" on every call. Cause: the model name is gone (`deepseek-chat` was retired), not load. Fix: list models with `GET https://api.deepseek.com/models` and use one that exists (`deepseek-v4-pro`, `deepseek-flash`).
- 2026-11-03 Symptom: the PWA keeps showing an old build, and `navigator.serviceWorker.register` fails, in the built-in browser pane. Cause: that embedded browser cannot fetch from a service worker. Fix: verify PWA install, offline and update behaviour on a real phone or desktop browser; in the pane, unregister the worker to see a new build.
- 2026-11-03 Symptom: in Svelte 5, an item pushed onto a list created with `state.x[k] ??= []` never renders. Cause: assigning to a `$state` object stores a reactive proxy, but `??=` returns the raw array. Fix: assign, then read the property back (see `list()` in `apps/web/src/lib/reduce.ts`). Tests must run in a DOM environment to catch this (`environment: 'happy-dom'`); under plain Node, Svelte compiles for the server and `$state`/`$effect` are inert.
- 2026-11-04 Symptom: a fake `gh` (or any shell script) written by a test or acceptance setup silently misbehaves, and a stray file appears at `/Users/mac/Documents/Code/AI`. Cause: this repository's path contains spaces ("AI Vibe Coding"), and an unquoted `$DIR` in the script splits there (`>> $R/gh.log`, `[ -f $R/x ]`). Fix: always double-quote paths in generated scripts; check for stray files after a run.
- 2026-11-04 Symptom: the supervisor answers "HTTP 401 ... Invalid credential" with the dev config. Cause: the OpenCode Go key (starts with `oc_`) was saved in `apps/core/.env` as `DEEPSEEK_API_KEY`, but `configs/hub.dev.yaml` reads `OPENCODE_API_KEY`, so no key was sent. Fix: check the variable name before blaming the key (`awk -F= '{print $1}' .env`); OpenCode Go also requires an `x-opencode-session` header, which `internal/provider/openai_chat.go` sends.
- 2026-11-04 Symptom: after rebuilding `build/web`, the workbench says "no such endpoint". Cause: a `sous-hub` started before the change is still running and serving the new web build. Fix: restart the Hub after changing core (`lsof -iTCP:8787 -sTCP:LISTEN` shows when it started).

