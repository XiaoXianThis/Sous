# scripts

Reusable tool scripts that serve more than one app. Scripts for a single app live inside that app.

## Rules

- Scripts come from `work/tmp/`: a one-off script moves here the second time it is needed.
- Start each script with a comment giving its purpose, usage, and dependencies.
- Scripts must run on Windows, macOS, and Linux. Prefer a cross-platform language. If a script depends on a shell, provide both `.sh` and `.ps1`.
- When scripts need dependency management (for example Node scripts), make an unpublished helper project in a subdirectory, such as `work/scripts/release/package.json`, and run its commands there.
- Kebab-case file names starting with a verb, such as `sync-version.py`.

## Scripts

| script | purpose | usage |
|---|---|---|
