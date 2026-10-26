# 第八轮对象存储与迁移

本轮通用 `BlobStore` 已实现 Put / Stat / OpenRange / Delete，覆盖图片生成 PNG 结果、上传／生成／演示媒体资产，以及 MP4、SRT、VTT 导出。FFmpeg、ASR、参考图、mask、口型任务读取经过租户资源授权和 SHA-256 校验的本地物化文件。浏览器仍经原有授权 HTTP 路由下载，支持 HTTP Range；不下发 S3 凭据、bucket URL 或签名 URL。即梦参考桥接是另一项明确授权的功能，不能混为一谈。

## 模式和信任边界

- `BLOB_STORE=file`（默认）：原有单目录文件兼容模式。PostgreSQL 启动必须明确选择 shared-file 或 s3，拒绝把每进程本地文件误作共享存储。
- `BLOB_STORE=shared-file`：显式 `BLOB_FILE_ROOT` 绝对路径，使用不可变共享文件 BlobStore。多个 API／worker 使用不同 `DATA_DIR` 作为私有 cache，配合 PostgreSQL manifest。它可用于本机真实多进程验收，但不是 S3 互操作验收。
- `BLOB_STORE=s3`：固定部署 endpoint，path-style S3 一般用途 bucket。变量：`BLOB_S3_ENDPOINT`、`BLOB_S3_BUCKET`、`BLOB_S3_REGION`、`BLOB_S3_ACCESS_KEY`、`BLOB_S3_SECRET_KEY`，可选 `BLOB_S3_SESSION_TOKEN`。不读取 AWS profile、实例元数据或 `.env`，不创建 bucket 或凭据。

S3 默认要求 HTTPS；只有显式 `BLOB_S3_ALLOW_LOOPBACK_HTTP=true` 才允许字面量 loopback IP 上 HTTP 合成测试服务。不允许 endpoint 用户信息、查询或路径；endpoint 不能由租户修改。不使用环境代理、不跟随重定向。部署方可配置私有 TLS S3 endpoint。SigV4 通过请求头签名，覆盖条件、SHA-256、metadata、Range，错误不会包含请求 URL、凭据或上游响应体。

对象 key 由 tenant hash / project hash / 随机 ID 组成。写入使用 `If-None-Match: *`，既有对象必须相同大小和哈希并读取验证，不能覆盖不同内容。不自动重试有不确定结果的 PUT。读取大小最多 128 MiB，全文件读取／物化验证与业务 manifest 一致的 SHA-256；range API 验证响应范围。暂不支持 multipart upload、S3 Express directory bucket、SSE-C 或自动凭据续期。

文件模式 manifest 存 `DATA_DIR/blobs.json`，必须随完整目录备份。PostgreSQL 模式使用 `studio_blobs`，每次读取查询 DB，包含 key、tenant/project hash、size、sha256 数据库约束，避免跨实例本地 manifest 陈旧。对象先完整持久，再提交 manifest，最后业务记录可见。业务失败可能留下孤儿对象；这不是跨数据库/S3原子事务，也没有自动 GC。所有输出保持不可变。

## 停服迁移与回滚

先停所有 API／worker，备份整个 DATA_DIR。迁移工具持有排他 `store.lock`，拒绝仍运行的文件实例、符号链接素材、未知路径、缺失／损坏文件、运行中导出。部署凭据须通过安全环境注入；以下命令不会配置或创建凭据：

```sh
cd backend
go run ./cmd/blob-migrate -data-dir /absolute/stopped/data -direction to-s3
```

工具读取已提交图片结果和媒体引用，逐对象上传并 GET 校验 SHA-256，再持久化 manifest；报告对象数、总字节数和 manifest 摘要，不输出 key／凭据。失败可以保留源文件与已验证 manifest；不删除源或远端对象。切换前检查报告及完整目录备份，启动设置 `BLOB_STORE=s3`。缺少任一旧引用的 manifest 会拒绝启动，不静默从本地逃逸回退。

切回文件模式前必须再停写，并对**当前元数据**执行：

```sh
go run ./cmd/blob-migrate -data-dir /absolute/stopped/data -direction to-file
```

它恢复包括切换后新增数据的所有当前引用；对已存在但哈希不同的本地文件拒绝覆盖。确认恢复后设置 `BLOB_STORE=file`。不能仅恢复切换前旧备份并宣称保留新写入。PostgreSQL 场景先使用数据库离线导出，包含 `ExportBlobManifest`，再进行对象恢复；导入 PostgreSQL 包括 `ImportBlobManifest`，对象迁移与数据库迁移仍需明确停写点。

## 验证与尚未证明的部分

`TestBlob*` 覆盖本地/S3协议合同、不可变写、签名字段、路径/endpoint、跨租户先拒绝、哈希校验、停服迁移、包含新增写入的回滚、真实 FFmpeg 输入重建及 MP4/SRT/VTT 回读、PNG结果重启回读。`STUDIO_BLOB_TEST_DSN` 显式注入合成测试 PostgreSQL，另测两实例同时写、独立cache、重启可见性、DB约束及manifest导入导出。

S3 HTTP fixture 不等于实际 S3 服务。2026-10-23 从官方 `https://dl.min.io/server/minio/release/linux-amd64/minio` 正常下载返回 HTTP 403（curl exit 22），没有启动 MinIO，没有改网络安全配置。前轮 DockerHub DNS 拉取失败亦未构成真实 S3 验收。真实 S3 互操作尚未通过；后续独立 rclone 服务演练见下节。

远端存储保留经过校验的私有本地 cache；现有 2 GiB scratch/cache 限制为每实例，不能宣称是 bucket 总容量配额。无自动 cache 淘汰、孤儿清理、对象版本恢复、远程备份或多区域容灾；部署需另设对象生命周期、容量与备份策略，不会由本工具擅自创建外部资源。

官方协议依据：[PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)、[GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)。

## 独立 S3 服务兼容性演练：rclone

除 fixture 外，本轮另安装上游 **rclone v1.75.2**，仅启动 loopback 的独立 `serve s3` 服务。上游下载来源为 [GitHub 官方 release binary](https://github.com/rclone/rclone/releases/download/v1.75.2/rclone-v1.75.2-linux-amd64.zip)，按同一 release 的 [SHA256SUMS](https://github.com/rclone/rclone/releases/download/v1.75.2/SHA256SUMS) 核验，ZIP SHA256：`349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702`。服务使用空 `--config`、独立临时数据/cache 目录、只含 PATH 的环境及合成 `--auth-key`，未读取已有 rclone 配置或任何真实 S3 凭据。

官方直接下载站返回 HTTP403；Debian sid 的 1.75.2-2 包虽然 SHA 核验成功，但构建未包含 `serve s3` 子命令，故未作为服务使用。上游 GitHub release 可正常获取，没有改动任何网络安全配置。

**当前 rclone 服务不兼容，不能作为本应用部署后端。** 实际应用适配器向既有 key 发送 `If-None-Match: *` 和不同内容时，服务接受写入，未提供所需的原子“不存在才写”约束。测试不会改为 HEAD 后再 PUT，这存在竞争窗口，不能替代原子条件写。`TestBlobS3Service` 为显式集成门禁；该合同未通过时不运行后续真实服务媒体测试，也不将其计作 S3 验收成功。

新 S3 兼容服务必须先以隔离测试 bucket 和合成数据运行服务合同（immutable PUT、SHA metadata、Range、SigV4、匿名拒绝、删除），全部通过后才能连接应用业务写入。名称宣称“S3兼容”或配置可保存不代表这些条件已获保证。

本次精确结果（`TestBlobS3Service`，exit 1）：

| 检查 | 实际响应／结果 |
| --- | --- |
| Signed PUT、HEAD SHA metadata、完整 GET | 通过 |
| Range `bytes=2-7` | HTTP 200，未满足要求的 206，适配器拒绝 |
| 错误 SigV4 secret | HTTP 403，拒绝 |
| 匿名 GET | HTTP 400，拒绝且无对象内容；与门禁期望的 401/403 不同，不是数据泄露 |
| 条件覆盖 PUT | HTTP 200，实际覆盖，严重不兼容 |
| DELETE 后 HEAD | 404 |
| 实际服务媒体导出 | 因合同失败未运行 |

条件覆盖前 SHA-256：`b6a01915659b4cdbf3e853c2bc68ef5877aad97c9b4b863179dff9c143888a85`；覆盖后 SHA-256：`3a988af6899a7eb8bdec4eb9d599b5e31de1bd8b4c862c53ee7d6bfdb14b5a0f`。仅含合成测试内容。完整本地日志 `/tmp/studio-s3-service-compat.log`。

测试入口（使用已启动且带固定合成凭据的 loopback 服务，bucket 为 `studio`）：

```sh
cd backend
STUDIO_S3_SERVICE_ENDPOINT=http://127.0.0.1:55480 go test -race ./internal/studio -run '^TestBlobS3Service$' -count=1 -v
```

此入口仅允许 loopback HTTP 合成测试，默认未指定 endpoint 则明确 skip；不会读取真实 S3 凭据。云 S3 的真实账户验证仍需另外明确授权和安全配置，不能把本次独立软件实测或其他 fixture 结果算成云验收。

## 2026-10-26 再核：当前环境仍缺合格独立服务

本次针对 `d633c295a149a6e99379922885f913fbeb395d36` 重新检查，并未把历史网络错误当作当前结果：

- `command -v minio garage seaweedfs weed rclone docker` 仅找到 `/usr/bin/rclone` 和 `/usr/local/bin/docker`；工作目录和临时目录中没有可用 MinIO、Garage 或 SeaweedFS 二进制。
- 使用新建空 `DOCKER_CONFIG` 执行 `docker image ls`，仅有 `postgres:17.6-alpine`（`d741b3768746`），没有可离线启动的 S3 镜像，没有读取既有 registry 凭据。
- 系统 `rclone version` 是 `v1.60.1-DEV`；`rclone --config /dev/null serve s3 --help` 只列出 dlna/docker/http/restic/sftp/webdav，没有 S3 服务。前轮独立安装的 1.75.2 实测仍是上节已记录的不兼容实现，未降低合同要求重新采用。
- [MinIO 官方 release](https://github.com/minio/minio/releases/tag/RELEASE.2025-10-15T17-29-55Z) 提供固定版本源码安装方式；[官方仓库](https://github.com/minio/minio) 说明社区版仅分发源码。因此本次执行正常的 `go install github.com/minio/minio@RELEASE.2025-10-15T17-29-55Z`，并未重试之前返回 403 的二进制下载地址。
- Go 成功解析版本 `v0.0.0-20251015172955-9e49d5e7a648`，但 `proxy.golang.org` 重定向到 `storage.googleapis.com` 的该模块 ZIP 下载返回 **Forbidden**，命令 exit 1。日志为 `/tmp/studio-s3-minio-build.log`。`/tmp/studio-s3-bin/minio` 不存在，未启动服务。该访问被拒绝后停止，没有更改 DNS、代理、安全设置或尝试其他地址绕过拒绝。
- [Garage 官方已知问题](https://garagehq.deuxfleurs.fr/documentation/reference-manual/known-issues/) 明确不支持 `If-None-Match` 等条件写，因此不把 Garage 作为通过此合同的候选，也未安装它。

**结果：实际 S3 服务合同和实际 S3 媒体链本次均未运行，不是通过，也不是新的适配器测试失败。** 本地适配器和 fixture 的既有通过结果不证明 AWS S3 或任何独立 S3 服务已合格。当前可恢复前提是提供经审核的本地 S3 服务二进制/镜像，或恢复官方源码及依赖的正常下载权限；不需要真实云凭据即可使用 loopback、独立临时目录和合成凭据执行现有严格门禁。真实外部 S3 endpoint、隔离 bucket 和授权凭据仍属另一项待确认的云验证，本次未配置。

后续必须保留 `TestBlobS3Service` 的原子条件写、Range 206、签名与匿名拒绝、SHA 元数据及真实媒体回读检查；不能用先 HEAD 后 PUT、接受 Range 200 或仅启动成功代替验收。本次仅更新证据文档，没有改动适配器、测试断言或运行生产部署。
