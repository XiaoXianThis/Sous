# build

Final build output. Nothing here is committed except this file.

## Rules

- One directory per app: `build/<app>/`. Build tasks write to it through `$WOK_OUT`.
- Only final output: executables, installers, static sites, library packages. Intermediate output stays inside the app.
- Only build tasks write here. Never put files here by hand.
- Other apps may consume this output. Record the dependency in both apps' `AGENTS.md`.
- Safe to delete at any time. Building again restores it.
