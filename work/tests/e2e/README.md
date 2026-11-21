# e2e

End-to-end tests: complete flows from the user's point of view.

## Running

- [Sous Supervisor Benchmark](sous-bench/README.md)：在 `sous-bench/` 运行 `bun run test` 或 `bun run bench run --mode offline --version v0.2.4`。runner 从 Git ref 构建并启动应用，无需预运行 Hub、Node 或前端。
- [本机工作区浏览器验收](local-workspace/README.md)：先构建 Core 和 WebUI，然后验证真实浏览器中的 Passkey、消息持久化、重启、锁定、窄屏与无外部请求；同一套测试也覆盖独立 HTTPS 云工作区与本机 Microsandbox。
