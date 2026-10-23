# 第八轮对象存储与迁移

本轮通用 `BlobStore` 已实现 Put / Stat / OpenRange / Delete，覆盖图片生成 PNG 结果、上传／生成／演示媒体资产，以及 MP4、SRT、VTT 导出。FFmpeg、ASR、参考图、mask、口型任务读取经过租户资源授权和 SHA-256 校验的本地物化文件。浏览器仍经原有授权 HTTP 路由下载，支持 HTTP Range；不下发 S3 凭据、bucket URL 或签名 URL。即梦参考桥接是另一项明确授权的功能，不能混为一谈。

## 模式和信任边界

- `BLOB_STORE=file`（默认）：原有单目录文件兼容模式。
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

S3 HTTP fixture 不等于实际 S3 服务。2026-10-23 从官方 `https://dl.min.io/server/minio/release/linux-amd64/minio` 正常下载返回 HTTP 403（curl exit 22），没有启动 MinIO，没有改网络安全配置。前轮 DockerHub DNS 拉取失败亦未构成真实 S3 验收。真实 S3 互操作仍待运行。

远端存储保留经过校验的私有本地 cache；现有 2 GiB scratch/cache 限制为每实例，不能宣称是 bucket 总容量配额。无自动 cache 淘汰、孤儿清理、对象版本恢复、远程备份或多区域容灾；部署需另设对象生命周期、容量与备份策略，不会由本工具擅自创建外部资源。

官方协议依据：[PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)、[GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)。
