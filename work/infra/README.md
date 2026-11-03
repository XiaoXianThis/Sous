# infra

Infrastructure across apps: local environment orchestration, deployment configs, CI helper scripts. A `Dockerfile` for a single app lives inside that app.

## Rules

- Organize by purpose, such as `docker/` (compose files for local development), `deploy/` (deployment configs), `ci/` (scripts that CI calls).
- CI entry files must live where the platform requires (such as `.github/workflows/`, which needs user approval). Entry files only orchestrate; the logic lives in `ci/`.
- No secrets. Use environment variables or placeholders.
- Document common commands and their working directory below.

## Commands

Deploying the Hub is described in `docs/guides/deploy-hub.md`.

| path | purpose |
|---|---|
| `deploy/hub/sous-hub.service` | systemd unit for the Hub |
| `deploy/hub/sous-hub-backup.service`, `.timer` | daily database snapshot via `sous-hub admin backup`, keeps 14 days |
| `deploy/hub/caddyfile` | TLS reverse proxy for agent.wxian.cc |
