# GitHub 版本管理与应用更新

Octopus 使用 Git 管理代码历史、分支和发布标签，使用 GitHub Releases 保存发布说明和安装包。GitHub Actions 在推送 `v*` 标签后构建 Windows 安装包及 Apple Silicon / Intel Mac 的 DMG。

## 应用内入口

进入「设置 → 版本信息」：

- 查看当前程序版本、提交记录和构建时间。
- 点击「检查更新」，查看 GitHub 上的最新正式版。
- 展开「更新说明」或「发布历史」，查看最近十个版本及其安装包。
- 点击对应平台的安装包下载；桌面端会打开系统浏览器，随后按安装程序完成更新。

发布历史保留预发布版本。正式更新使用 GitHub 的 `/releases/latest` 接口，因此较新的测试版不会覆盖正式版更新提示。开发构建及无法识别的版本会显示相应状态。

## 发布源与网络设置

默认发布源为 `https://github.com/CE-ND/octopus`，应用显示的仓库链接、发布历史和下载地址使用同一来源。

可在启动后端前设置环境变量：

| 环境变量 | 用途 |
|---|---|
| `OCTOPUS_GITHUB_REPO` | 指定其他 GitHub fork，例如 `https://github.com/your-account/octopus` |
| `OCTOPUS_GITHUB_PAT` | 可选 GitHub 令牌，用于访问私有仓库或提高查询限额；令牌只在后端使用 |
| `HTTP_PROXY` / `HTTPS_PROXY` | 查询 GitHub 时使用的环境代理 |

应用「网络与服务」中配置的系统代理也用于版本查询，支持 HTTP、HTTPS 和 SOCKS5。

发布数据缓存十分钟；手动检查可以刷新缓存，但两次外部查询至少间隔三十秒。无法连接 GitHub 或达到查询限额时，页面会显示失败提示，并保留发布页面入口。

## 发布新版本

1. 在 `release-notes/` 保存对应版本的更新说明，例如 `release-notes/v0.1.7.md`；没有该文件时由 GitHub 自动生成发布说明。
2. 完成代码人工审查、后端测试和前端检查，将待发布代码提交到 `dev`。
3. 为该提交创建版本标签，例如 `v0.1.7`，并推送标签到 GitHub。
4. 等待 `.github/workflows/release.yaml` 的 Windows 和两种 macOS 打包任务完成，发布任务会收集全部安装包后统一创建 Release。
5. 在 GitHub Releases 核对安装包、正式版标记和本次发布说明。
6. 已安装的 Octopus 在下一次检查更新时会发现新版本。

构建流程通过 `scripts/sync-release-version.js` 从标签同步安装包版本，并把相同版本注入 Go 后端和前端。

## 管理接口

接口均需要管理端认证，响应沿用 `{code, message, data}` 格式：

| 接口 | 返回内容 |
|---|---|
| `GET /api/v1/update/now-version` | 当前版本字符串，保留原有兼容接口 |
| `GET /api/v1/update/info` | 当前版本、提交、构建时间、平台和发布仓库 |
| `GET /api/v1/update/releases` | 更新状态、最新正式版、最近十个发布版本及可下载文件 |
| `GET /api/v1/update/releases?refresh=true` | 手动刷新发布信息 |
