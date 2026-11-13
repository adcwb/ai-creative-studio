# Windows 原生后端运行与迁移

本页适用于原生 Windows amd64 API 与 PostgreSQL worker，不依赖 WSL。Linux 原有运行方式保留。交叉编译只证明能生成 `.exe`；实际 Windows 内核、SCM、ACL、编码器的结果必须以对应提交的 `Windows native / file`、`Windows native / postgres` CI 为准。

## 构建与前置条件

1. 安装 Go（版本以 `backend/go.mod` 为准）、Git 和可信的 Windows FFmpeg/ffprobe。FFmpeg 需支持 libx264、AAC、subtitles；把两者所在目录加入当前账号或服务的 PATH。
2. PostgreSQL 模式需可用的 PostgreSQL 服务。备份/恢复工具需同版本 Windows `pg_dump.exe`/`pg_restore.exe`。Go 运行服务不需要 Python；验收脚本需要 Python 3。
3. 在 PowerShell 的仓库目录构建：

```powershell
Set-Location backend
New-Item bin -ItemType Directory -Force | Out-Null
Get-ChildItem cmd -Directory | ForEach-Object {
  go build -o (Join-Path bin ($_.Name + '.exe')) ('./cmd/' + $_.Name)
  if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
}
```

数据目录、KEK 和配置必须受 NTFS ACL 保护，不能用 `chmod 0600` 的显示值当作 Windows 安全证明。程序为新目录设置当前运行身份、SYSTEM、管理员的私有 DACL；已有非空公开目录会拒绝启动，**不会自动重授权或跳过数据**。使用新的私有目录复制旧数据；不要直接给公共目录运行递归授权覆盖。

新部署建议 API/worker 使用同一专用 Windows 身份。若使用不同身份，需由管理员授予所需私有目录/共享 BlobStore 访问权；只允许经过实际私有父 ACL 验证的继承授权，不能仅凭继承标志信任任意主体。

## 控制台运行

```powershell
$env:DATA_DIR='C:\CreativeStudio\data'
$env:HTTP_ADDR='127.0.0.1:8080'
$env:FRONTEND_ORIGIN='http://localhost:5173'
$env:AUTH_MODE='local'
.\bin\server.exe
```

以上仅配置运行入口，模型、认证、可信源等仍按正常部署文档配置。`.env` 不会自动加载。配置 JSON 必须为 UTF-8，无 BOM；Windows PowerShell 5 的默认 `Out-File` 编码不能直接用于 JSON。建议用系统设置保存后端配置，敏感值保持脱敏；环境变量中的秘密也应遵循最小可见范围。

文件模式在新目录生成并持久保存私有 `model-config.key`，已有密文而 KEK 丢失时继续拒绝解密。不要重新生成密钥掩盖丢失问题。迁移必须携带原 KEK，或使用同一 `MODEL_CONFIG_KEK_BASE64`/`MODEL_CONFIG_KEK_FILE`。备份 KEK 应与业务备份分开保护。

PostgreSQL API/worker 使用各自的本地 `DATA_DIR`，设置 `STORAGE_BACKEND=postgres`、`QUEUE_BACKEND=postgres`、同一数据库和 KEK，以及显式 `BLOB_STORE=shared-file` / `BLOB_FILE_ROOT` 或现有 S3 配置。不要让两个进程共用本地缓存目录。生产迁移使用原有显式迁移命令；CI 的 `STORAGE_AUTO_MIGRATE=true` 仅用于一次性测试数据库。

## Windows 服务

原生 SCM 名称固定为 `CreativeStudioAPI`、`CreativeStudioWorker`。用管理员 PowerShell 安装，例如：

```powershell
New-Service -Name CreativeStudioAPI -BinaryPathName '"C:\CreativeStudio\bin\server.exe"' -StartupType Manual
New-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Services\CreativeStudioAPI' -Name Environment -PropertyType MultiString -Value @(
  'DATA_DIR=C:\CreativeStudio\api-data',
  'HTTP_ADDR=127.0.0.1:8080',
  'AUTH_MODE=local',
  'FRONTEND_ORIGIN=https://your-studio.example'
) -Force
Start-Service CreativeStudioAPI
Get-Service CreativeStudioAPI
Stop-Service CreativeStudioAPI
```

Worker 用 `worker.exe` 和 `CreativeStudioWorker`，配置独立缓存及 PostgreSQL/共享素材。示例默认服务账号为 SYSTEM；生产应按组织规范选定服务身份、数据 ACL 和网络权限。注册表环境值可能包含秘密，须限制服务注册表读取权限，不得把真实环境导出到工单/日志。

SCM 模式要求绝对 `DATA_DIR`，会把工作目录设为该目录，避免相对路径落在 System32。安装前把工具和相对配置改成绝对路径。SCM Stop/Shutdown 会取消同一 API/worker 上下文并等待退出，不能用 `Running` 代替 HTTP `/healthz` 与数据库健康检查。

SCM 日志分别追加到私有 `DATA_DIR/CreativeStudioAPI.jsonl`、`CreativeStudioWorker.jsonl`，保留现有安全结构化诊断；控制台仍写 stderr。运维应在停服时归档日志，或采用组织的日志收集/轮换策略，不要向公共目录复制秘密。`scripts/windows_service_fixture.ps1` 仅供一次性 CI，拒绝覆盖已有同名服务，不应拿来管理生产服务。

## 外部工具与持久化边界

- 本地模型/审核适配器在 Windows 要求管理员配置的绝对 `.exe`。不能把 `.sh`、`.cmd`、`.bat`、`.ps1` 直接当作模型入口；需要原生 launcher 并负责其模型/GPU/依赖。没有为兼容而放开浏览器任意命令执行。
- Windows 工具创建时先挂起，加入带 kill-on-close 的 Job Object 后再运行；取消会终止子树，分配失败不会无保护运行。最小环境保留系统路径和私有临时目录，不继承模型密钥、数据库配置等无关环境。
- 文件锁使用 LockFileEx；替换使用 MoveFileEx write-through；读结果继续拒绝 symlink/reparse 与 Root 逃逸。Windows 长路径支持不代表每个外部编码器都支持长路径，部署目录建议较短。
- **Windows 没有 POSIX 目录 fsync 的相同语义。** 文件数据刷新、write-through 替换及 NTFS 日志不等于 Linux 同等的系统断电保证。程序为此记录一次明确告警，不吞掉普通文件刷新错误。进程崩溃恢复与机器断电耐久性是两类验证。
- 本轮不改变数据库 DDL、JSON 版本、认证/RBAC、租户隔离、模型能力或费用/额度策略。真实供应商、商业许可、本地模型效果不能从合成测试推导。

## 迁移与回退

1. 停止所有 API、worker、定时备份等写入者；备份原数据库、完整 DATA_DIR、BlobStore 和 KEK。`-confirm-stopped` 只是操作者声明，不会自动停止所有 PostgreSQL writer。
2. 在原平台执行只读预检：`python scripts/check_windows_paths.py /absolute/old-data`。默认输出路径指纹；管理员可以加 `--show-paths` 显示文件名。检查保留名、大小写碰撞、链接、重解析点和特殊文件；不会重命名、读取正文或忽略记录。
3. 使用新的 Windows 私有数据根，完整复制数据和原 KEK。先在隔离环境检查任务、历史版本、素材、上传与回执，再切换服务。路径预检并不能证明数据一致性。
4. 回退需同时保留匹配的数据库、DATA_DIR、BlobStore 和 KEK。不可恢复活动目录的部分文件，不能把 PG 缓存直接当作文件模式数据。

## 验证证据

重建基线为 `be2116e`；原本地提交 `071ea6d` 在替换环境中不可访问，当前实现是新的重建提交，验证重新执行。具体运行记录见 [Windows 适配规格](superpowers/specs/2026-11-13-windows-native-rebuild.md)。

Windows CI 安装经 SHA256 固定校验的 FFmpeg，执行平台/持久化/命令竞态测试、可移植应用合同，以及 API/worker 控制台和实际 SCM、独立进程重启、幂等、跨空间拒绝和视频/音频/字幕完整解码。测试仅使用明确标记的合成 fixture，无真实模型调用。POSIX FIFO fixture 在 Windows 上单独跳过；Linux 保留全部原覆盖，Windows reparse/ACL/Job Object 有独立测试。任何 Windows job 失败、取消或跳过都会阻止发布镜像。

未验证范围包括不同 Windows 版本/架构、SMB/UNC/ReFS、机器断电、真实 S3、模型/GPU及付费供应商。以 CI 实际终态更新验证范围，不能把交叉编译当原生验收通过。

依据：[Go os.Root](https://pkg.go.dev/os#Root)、[LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)、[Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、[MoveFileEx](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)、[FlushFileBuffers](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers)。
