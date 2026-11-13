# Windows 原生后端重建规格与验收

日期：2026-11-13。基线：`be2116e13139c6eeea767734aad2714c2c487c1c`。
状态：实现已推送；实际 Windows 原生 CI 的修复与复验中。

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
- [x] 已安全快进合并到 master 并推送、核验 SHA；持续跟踪对应 HEAD 的 CI。
- [ ] Windows 实际文件/PG、SCM、ACL、工具验证和全套云端 CI 终态。

## 风险与兼容

无 schema/JSON 升级，不重建 KEK。既有非空公开 Windows 数据目录不能静默修 ACL；需管理员迁到私有目录。Windows 无 POSIX 目录 fsync 等价保证，明确记录耐久性差异。第三方工具不保证长路径、UNC/ReFS/网络盘或 GPU 运行；SMB/真实 S3/付费供应商不在合成验收证据内。操作文档覆盖账号、注册表秘密、服务日志和所有 writer 必须停写的迁移/回退步骤。

### 首轮实际 Windows CI（43345596648）

Windows 文件 job 已实际通过 nativefs（内核锁/长路径/ACL/继承授权拒绝）、nativeproc（Job 子树取消）、servicehost、持久化和领域基础竞态；随后 CLI 密钥轮换失败，尚未执行应用及 SCM 全链路，不能声称原生验收成功。补充离线轮换安全阶段诊断和发布前关闭读取句柄，保留独占锁与所有隐私校验。

PostgreSQL job 在 runner 预装服务启动失败；改用预装工具初始化全新 RUNNER_TEMP 测试集群，限定 loopback:55439，并在 always 步骤停止，不依赖预装服务账号/状态。PowerShell AST 和固定 SHA256 FFmpeg 安装已在 Windows 实际通过。补齐 SCM 运行 goroutine 的安全 panic 捕获和测试。

首轮 Linux file/PG 的 A–M 两组失败于加密备份 fixture：测试目录的显式 Unix chmod 被误替换为 MkdirAll（已有目录不会更改权限），在 runner 的 0022 umask 下仍为 0755。本地 0077 umask 隐藏了问题。恢复测试专用、按 OS 实际设置隐私的 helper；生产 Unix 的目录权限策略不变，补做 0022 umask 的轮换/备份回归。

本地 PostgreSQL 全套 `-race -count=1` 已通过（application 936.233 秒），文件原分组 A–M / other 已通过（application 316.714 / 213.077 秒）；最终 native 组仍在运行。0022 umask 下轮换与加密备份专项已通过。Windows 原生应用选择增加现有可恢复上传、系列、媒体、合成本地 S3 multipart、加密备份测试，不替代原有 Linux 全量检查，不将合成 S3 当作真实服务验收。

Linux 普通编码器/数据库工具保留原继承进程组，避免改变既有 supervisor/崩溃测试的整组终止语义；本地模型适配器仍使用原有的独立进程组取消。Windows 工具统一受 Job Object 管理。新增真实派生子进程取消测试覆盖显式隔离启动。

本地文件三组竞态全部通过（A–M 316.714、other 213.077、native 226.326 秒），PG 全套通过。0022 专项与新增进程测试已通过。Windows CI 将便宜的控制台/真实 SCM 纵向验收放在长竞态套件之前，便于尽早暴露服务/编码器错误，保留所有已有检查。

### 第二轮 Windows CI（43346610768）

独立 PostgreSQL 集群、轮换 CLI 和基础竞态均已实际通过。应用测试剩余三项失败来自旧测试内嵌 `/usr/bin/python3` 脚本，Windows 无法原生执行。保留 Linux 原 Python fixture；为 Windows 增加测试二进制的原生子进程入口，独立用 stdlib 流式解码并核对真实媒体 SHA、元数据、允许/拒绝及篡改拒绝，不调用生产 parser 来验证自身，不删除或跳过断言。新增便携 fixture 正反向合同测试。SCM 端到端仍需下一轮先执行。

原生审核 fixture 正反向与三个原失败路径在 Linux 本地竞态复验通过（52.970 秒），Windows 编译通过；保留内容、哈希、拒绝、无重试与清理断言。SCM fixture 对创建的服务写入当前 run/attempt 所有权标记，清理拒绝删除不属于该运行的同名服务。最终原生运行结果需看后续 CI，不从这些编译结果推导成功。

### 第三轮 Windows CI（43348462914）

提前的真实进程验收暴露了测试 launcher 环境白名单大小写问题：Windows Python 把环境键转为大写，原 `SystemRoot` 精确匹配漏掉 `SYSTEMROOT`，两个子进程的网络启动分别在 serve/storage 失败。改为大小写无关白名单并加单测，继续不继承秘密；启动进程若提前退出立即失败，避免空等 60 秒。该归因需下一轮真实运行确认。原生审核 fixture 改用独立复制的 PE 文件，避免 Windows 对仍被父测试进程映射的同一文件产生清理共享冲突。

### 第四轮 Windows CI（43349903630）

SYSTEMROOT 修复后实际 Windows 控制台 API 已启动并完成任务/重启/幂等/跨空间与项目阶段，验证了启动器修复。PG 独立 worker 在导出时退出，原日志仅 internal_error；追加固定 worker 阶段与任务种类、受约束的 OS 错误号/SQLSTATE/进程退出值诊断，不打印 SQL 内容、路径、错误正文或秘密，并补充泄露负向测试。根因仍待真实下一轮证据，不把该失败当验收通过。

### 第五轮 Windows CI（43350983520）

Windows file 全任务成功；PostgreSQL 控制台与真实 SCM 的重启、幂等、租户拒绝、独立 worker、实际视频/音频/字幕编码解码均成功。PG 应用剩余两项大媒体审核失败（10.85/12.17 秒）接近固有 10 秒命令边界；file 同项通过。将审核 fixture 从整个 race 应用测试映像改为无 race 的独立 stdlib 原生可执行文件，保留相同的独立流式解码/SHA/元数据/允许与拒绝断言和生产时限，不增加超时或跳过覆盖。上一轮 worker 退出未在本轮控制台/SCM复现，保留安全阶段诊断，原根因尚未确认。

原生 PG fixture 在首次空数据库初始化后，API 重启改为仅校验现有 schema，遵循显式离线迁移的部署合同，避免把在线 DDL 和运行恢复混为一项测试。未据此声称上一轮 worker 退出的根因已确认。
