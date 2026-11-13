# CC7 · 版本化发布资料与交付

状态：本地实现，尚未推送。此前CC5角色/所有权提交 `f076a06`；本文件描述后续发布资料增量，不代表个人作品迁移或团队关闭已完成。

## 用户故事与验收

创作者为某次已完成的正式成片准备标题、简介、标签和可选封面，明确核对素材传播权利后保存一个不可变版本。新版本保留旧版本，每次交付明确固定所选版本；后续剪辑、改标题或新封面不会悄悄改写历史发布包。此流程不联系外部平台，不代表平台完成内容审核或许可认证。没有资料的旧导出继续可以下载原交付包，不恢复费用、项目额度或审核就绪门槛。

- 已完成正式导出才可保存资料；预演或未完成导出拒绝。服务器要求当前项目空间的workspace.write和CSRF，不依赖界面按钮授权。只读成员可以读取已有资料和交付，其他空间404。
- 发布资料CAS独立于剪辑版本，按该export的expected_metadata_version递增；媒体状态和原请求回执同事务持久化。原请求返回原保存版本，即使后来已有新版本，也不能覆盖后来内容。相同键不同导出/内容/作者拒绝。
- 封面只接受本项目已保存的PNG/JPEG/WebP图片；固定素材ID、MIME和SHA-256。无任意URL、外部图片下载或浏览器Blob打包。准备和实际流式写入均核验字节；丢失/变化时不静默省略封面，也不留下宣称成功的完整ZIP。
- 桌面/手机可以编辑、预览封面、查看历史及下载指定版本。未确认权利时保存按钮禁用，服务器也拒绝。网络结果未知时字段锁定，保留原请求键和内容，刷新后可核对；未保存编辑通过既有导航保护。元数据请求锁定相同页面其他媒体写入；多个成片各有独立锁，不能相互解除未确认状态。

## 数据与契约

`media.MediaState.delivery_metadata[]` 为可选不可变记录，包含ID、export_id、version、title、description、tags、可选cover_asset_id/cover_sha256/cover_mime、rights_confirmed、created_at/by。旧数据不合成虚假确认；旧字段和摘要省略空新字段。文件模式现有原子媒体快照、PG现有studio_media_projects和studio_media_receipts承载，不新增没有约束的浏览器权威或生产迁移命令。

- `GET /api/v1/projects/{id}/media/exports/{export_id}/delivery-metadata`：媒体版本和发布资料历史。
- `POST` 同路径：`expected_metadata_version`、title、description、tags、可选cover_asset_id、rights_confirmed=true；Idempotency-Key必填；版本0表示首次保存。
- 单集`delivery.zip?metadata_version=N`固定指定资料；省略时取请求时的最新资料，旧导出没有资料仍兼容。ZIP manifest附publishing_metadata，可选cover文件列入实际文件校验清单。
- 整季预览选择摘要包含每集选定的发布资料；保存新发布版本会使旧预览SHA失效。包内按固定选择流式写入元数据和封面，不因某集缺失封面而宣称整季完整。

标题200字符、简介12000字符及单个标签80字符是当前元数据输入契约；整个JSON仍受现有HTTP元数据工作集保护，不能据此声称视频/项目/集数有额度。没有标签总数或发布次数产品配额。不同外部平台的字段规范/许可仍由创作者核对，本项目不虚构已验证的通用自动发布能力。

关键代码：`modules/publishing/delivery.go`、`application/delivery_metadata.go`、`media_delivery.go`、`season_delivery.go`、`media/model.go`；前端`components/DeliveryMetadata.tsx`、`MediaStudio.tsx`、`features/series/SeasonDelivery.tsx`；API见`api/openapi.json`。

## 升级与边界

升级前保存匹配的数据库及DATA_DIR备份，协调停止所有旧API/worker writer后使用匹配的新版本；旧二进制可能忽略并在写入时丢弃新可选字段，**不支持新旧writer混跑**。回退需恢复匹配备份，不能只切换镜像。未在生产执行升级。PG文件往返使用现有媒体状态导出格式；新字段不能在跨空间复制时遗漏，CC6必须显式重映射导出、封面和版本ID并保留来源。

个人→团队复制、团队解散、商业订阅/支付、邮件服务、真实5–10集制作与外部平台验收未因本增量自动完成。历史资料/成片可按现有授权读取；rights_confirmed仅记录本次作者声明，不能代替外部商业许可核验。

## 验证

- 文件模式完整 `go test ./... -count=1` 通过，application 163.722秒。最终本地真实PG `go test -race ./internal/application ./internal/modules/publishing -run 'DeliveryMetadata|SeasonDelivery|TestDeliveryRejects' -count=1 -timeout=120s` 通过（application 8.346秒），覆盖版本与原请求恢复、跨导出键冲突、权利确认、封面当前项目身份/字节变化/准备与传输间变化、整季预览失效、缺失封面、CSRF/只读/跨租户等。
- 前端24文件79单测通过；最终类型、Lint、生产构建通过（Vite 3.07秒）。OpenAPI结构验证、3037条静态国际化检查通过；未声称逐一抓取所有HTTP响应进行schema验证。
- 创作者浏览器桌面6项（26.5秒）、手机6项（29.0秒）通过；最终锁定/封面校验版本再单独复验发布闭环桌面1项（13.1秒）、手机1项（14.1秒）。包含实际FFmpeg合成、真实登录/接口、响应丢失刷新恢复、保存历史版本和指定版本ZIP下载。首次浏览器运行误用了构建完成前的旧二进制而404，等待新构建结束后复跑通过；未增加超时或删除断言。
- 桌面1920×1080和iPhone13完整页面及发布区域截图已实际查看，保存在 `/workspace/artifacts/creator-completion/creator-publishing-{metadata,panel}-{desktop,mobile}.png`。没有横向溢出；样式沿用现有字体、表单及配色。
- Go vet及Windows amd64 server/worker交叉编译通过；没有Windows原生验收。未运行本阶段云端CI、真实S3/付费供应商/商业许可或生产迁移，不将旧已发布基线的CI当成本阶段证据。

测试使用本地合成素材和实际FFmpeg，不使用付费供应商、生产配置或外部发布。本地阶段完成后继续CC6，未提前推送。
