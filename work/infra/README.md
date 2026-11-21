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
| `deploy/hosted/` | 独立云 Hub 预览的 HTTPS 反代、单权威 systemd 服务和配置样例；见 `docs/guides/hosted-workspace.md` |
| `deploy/hosted/sous-ec2-worker.service`、`ec2-driver.example.json` | 托管容量的 EC2 worker 镜像启动单元与驱动配置样例；仍需真实 KVM 和 AWS 资源验收 |
