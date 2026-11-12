# infra

Infrastructure across apps: local environment orchestration, deployment configs, CI helper scripts. A `Dockerfile` for a single app lives inside that app.

## Rules

- Organize by purpose, such as `docker/` (compose files for local development), `deploy/` (deployment configs), `ci/` (scripts that CI calls).
- CI entry files must live where the platform requires (such as `.github/workflows/`, which needs user approval). Entry files only orchestrate; the logic lives in `ci/`.
- No secrets. Use environment variables or placeholders.
- Document common commands and their working directory below.

## Commands

Deploying is described in `docs/guides/deploy.md`.

| path | purpose |
|---|---|
| `deploy/relay/sous-relay.service` | systemd unit for sous-relay (the server of 0.3) |
| `deploy/relay/caddyfile` | TLS reverse proxy for the relay |
