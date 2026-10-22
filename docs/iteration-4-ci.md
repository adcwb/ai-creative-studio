# 第四轮 Actions 运行时维护

2026-10-22 按用户提供的 warning 核查。第三轮 CI 本身成功；warning 表示 Actions 声明的 JavaScript 运行时仍为 Node 20，而 runner 已强制按 Node 24 执行。项目 `node-version: '24'` 只控制后续 npm/构建命令，不能更改每个 Action 的 `runs.using`。

依据 [GitHub 官方弃用说明](https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/)，升级至正式支持 Node 24 的 Action。没有使用强制兼容变量、不安全 Node 版本开关或隐藏 warning。

## 已核查的稳定版本与锁定

通过官方仓库 Releases latest、对应 tag README、action.yml 与 tag 对应 commit 核实，四个 action.yml 均明确 `runs.using: node24`。CI 使用完整 commit SHA，注释标版本；不是猜测 major tag。

| Action | 原版本 | 核查时最新稳定版 | 固定 SHA |
| --- | --- | --- | --- |
| checkout | v4 | [v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1) | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| setup-go | v5 | [v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0) | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| setup-node | v4 | [v7.1.0](https://github.com/actions/setup-node/releases/tag/v7.1.0) | `949feb2413d6458794dcd2491c4babbbce0c15c1` |
| setup-python | v5 | [v7.1.0](https://github.com/actions/setup-python/releases/tag/v7.1.0) | `6d4fd22b20465ccc0f7354476588e8df7c9ca0e0` |

## 兼容性核查

- Runner：四个 [README](https://github.com/actions/checkout/blob/v7.0.1/README.md) 所述 Node 24 最低 runner 为 v2.327.1。checkout 的 Docker container action 内认证 Git 场景需 v2.329.0；本项目没有该场景。上一成功运行日志实际 runner 为 **2.338.0**，当前继续使用 GitHub 托管 `ubuntu-24.04`，不切换运行环境。
- checkout：[v7 说明](https://github.com/actions/checkout/blob/v7.0.1/README.md) 包含 ESM 升级及对 `pull_request_target`/`workflow_run` 下 fork 检出的默认限制。当前只用 `push` 与 `pull_request`，不启用 `allow-unsafe-pr-checkout`。v6 后持久凭据存放于 RUNNER_TEMP 下独立文件；现有默认检出、浅克隆和 job 后清理继续保留。未添加认证或写权限。
- setup-go：[官方 README](https://github.com/actions/setup-go/blob/v7.0.0/README.md) 说明 v7 ESM 迁移不改输入/输出，Node24 从 v6 起支持；缓存默认输入变化不影响本项目，因为已显式指定 `cache-dependency-path: backend/go.sum`。保留 `go-version-file: backend/go.mod`，Go 依赖版本不随此次维护改变。
- setup-node：[官方 README](https://github.com/actions/setup-node/blob/v7.1.0/README.md) 说明自动 npm 缓存及 dummy NODE_AUTH_TOKEN 移除。本项目已有显式 `cache: npm` 和锁文件路径，继续只缓存包管理器数据、不缓存 node_modules；未设 registry-url/认证令牌、不做 npm 发布，因此不依赖已移除的假令牌。保留项目 Node 24。
- setup-python：[官方 README](https://github.com/actions/setup-python/blob/v7.1.0/README.md) 说明 v7 ESM 迁移不改现有输入/输出行为。保留 Python 3.12；没有启用新增 mirror/mirror-token 或 pip 缓存，仍用官方默认发行源。
- 权限继续只有 `contents: read`，不增加 secrets、OIDC、外部服务或部署步骤；已有测试及断言保留。第四轮新增业务检查单独接入，不取代原检查。

## 验证边界

本地已解析 workflow YAML，核对全部 SHA 长度、现有 setup 输入、runner 和 permissions 不变；官方 tag/manifest 核查通过。此时未推送第四轮，因此无法声称新 Action 在远端已执行或 warning 已在运行中消失。待用户授权发布后，需核查该提交 Actions 终态及日志；没有通过反复推送只验证配置。
