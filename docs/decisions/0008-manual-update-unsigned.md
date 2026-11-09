# 0008 手动更新，不做代码签名

- Status: Accepted（「不做代码签名」被 0026 取代）
- Date: 2026-11-03

## Context

作者没有 Apple 开发者账号，sous 目前只给作者一个人用。

## Decision

- 不做自动更新。用户在界面上点击“检查更新”，daemon 从 GitHub Releases 下载新版本并替换自身。
- GitHub Actions 在打 tag 时构建三个平台的产物，发布到 Releases。
- 不做代码签名，也不做 macOS 公证。程序自己下载的文件不带隔离标记，所以首次手动安装之后，更新不会被系统拦截。

## Alternatives

- 签名、公证并自动更新：需要付费账号，对单人使用来说没有必要。

## Consequences

- 首次在 macOS 上安装需要右键打开；在 Windows 上会出现 SmartScreen 提示。
- 如果以后要给别人用，需要重新评估这项决定。
