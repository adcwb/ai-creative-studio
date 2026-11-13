# Windows 原生后端重建规格与验收

日期：2026-11-13。基线：`be2116e13139c6eeea767734aad2714c2c487c1c`。
状态：实现与本地验证中；实际 Windows CI 尚待提交后执行。

## 范围与证据

用户授权将 Windows 适配合并至 master 并推送，跟踪完整 CI；不部署生产。原环境不可用，旧提交 `071ea6d562b3a83f85e426e2e42a9c3ff3aa9a0b` 未上传，替换环境 `git cat-file` 和 GitHub API 均找不到它。新建独立工作区从远端 master 重建，保留原 `work` 分支 `77d5a98`，不假装恢复旧提交，不沿用旧验证结果。

原实现中的 O_NOFOLLOW、Flock、Renameat2、Setpgid、Unix chmod、固定 PATH、目录 Sync 和 FFmpeg 路径是实际 OS 依赖。集中在 nativefs/nativeproc/servicehost 适配，业务持久化和 API 合同保持一致。

## 需求、实现与可测试验收

| 需求 | 代码 | 验收 |
| --- | --- | --- |
| FR-W1 原生文件锁、安全读写和替换 | `backend/internal/platform/nativefs` 及全部持久化调用方 | 两个真实进程争锁，释放后可重入；最终链接拒绝；no-replace 不能覆盖目录 |
| FR-W2 Windows 私有数据与密钥 | nativefs DACL、deployconfig、auth/model、审计 | 公开非空目录/文件拒绝且不修改数据；当前账号/SYSTEM/Admins/实际私有父授权；不依赖 chmod 位 |
| FR-W3 Windows 工具、取消与路径 | nativeproc、媒体渲染、备份、本地适配器 | suspended→Job→resume，无保护分配失败即拒绝；取消杀死真实子树；不继承合成秘密；Unicode/空格/引号路径实际编码 |
| FR-W4 API/worker 控制台与 SCM | servicehost 及 cmd/server、cmd/worker | 绝对 DATA_DIR，SCM Stop/Shutdown 取消生命周期，私有日志；独立 PG worker 执行及重启 |
| FR-W5 安装、配置、离线迁移 | `docs/windows-native.md`、只读路径预检 | 保留名/大小写/链接/特殊类型报错，文件不变；完整原 KEK，未做隐式 DDL |
| NFR-W1 Linux 合同与安全不退化 | Linux 原 syscall、全部现有 CI 保留 | 文件/PG 竞态回归、认证/RBAC/幂等/安全及媒体原覆盖继续通过 |
| NFR-W2 证据真实 | Windows 双存储 matrix，verify 必需依赖 | 不把编译当执行；失败/取消/跳过不能发布；不声称真实付费模型/GPU效果 |

## 任务与运行记录

- [x] 独立工作区和基线核对；保留旧快照。
- [x] nativefs、nativeproc、SCM 与实际业务调用方重建。
- [x] Windows 命令及全部测试包交叉编译（`-exec=true` 仅编译，不执行）。
- [x] 新平台内核锁与进程树测试在 Linux 运行通过。
- [x] Linux 控制台 file 与 PostgreSQL API/独立 worker 原生进程脚本通过：重启、原键回放、跨空间拒绝、实际视频/音频/字幕编码与完整解码，`live_provider_calls=0`。
- [x] 23 项 Python 脚本测试通过。
- [x] Go vet、格式、差异与 actionlint 通过；最终 Windows 命令及全部测试包交叉编译通过。
- [ ] Linux 文件与 PG 全量竞态：基础平台/持久化/命令及受影响锁/轮换/加密备份专项已通过。文件模式单次全套触发默认 10 分钟总超时，超时前无断言失败，正在 `TestGatewayLargeVideoDownloadsIntoReviewedAssetWithoutGenerationRetry`；改用仓库原有三组继续，未修改 CI 超时或跳过覆盖。PG 全套仍在运行。
- [ ] 合并/推送并核验远端精确 SHA。
- [ ] Windows 实际文件/PG、SCM、ACL、工具验证和全套云端 CI 终态。

## 风险与兼容

无 schema/JSON 升级，不重建 KEK。既有非空公开 Windows 数据目录不能静默修 ACL；需管理员迁到私有目录。Windows 无 POSIX 目录 fsync 等价保证，明确记录耐久性差异。第三方工具不保证长路径、UNC/ReFS/网络盘或 GPU 运行；SMB/真实 S3/付费供应商不在合成验收证据内。操作文档覆盖账号、注册表秘密、服务日志和所有 writer 必须停写的迁移/回退步骤。

### 首轮实际 Windows CI（43345596648）

Windows 文件 job 已实际通过 nativefs（内核锁/长路径/ACL/继承授权拒绝）、nativeproc（Job 子树取消）、servicehost、持久化和领域基础竞态；随后 CLI 密钥轮换失败，尚未执行应用及 SCM 全链路，不能声称原生验收成功。补充离线轮换安全阶段诊断和发布前关闭读取句柄，保留独占锁与所有隐私校验。

PostgreSQL job 在 runner 预装服务启动失败；改用预装工具初始化全新 RUNNER_TEMP 测试集群，限定 loopback:55439，并在 always 步骤停止，不依赖预装服务账号/状态。PowerShell AST 和固定 SHA256 FFmpeg 安装已在 Windows 实际通过。补齐 SCM 运行 goroutine 的安全 panic 捕获和测试。
