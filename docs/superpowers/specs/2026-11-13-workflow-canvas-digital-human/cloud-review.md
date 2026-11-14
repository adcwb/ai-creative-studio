# 专用云数字人与人工审片增量

状态：2026-11-14，本地实现与验证；基线96b8047399e9ab11350ce0e7d334a56656972b1e。不推送、不部署，不调用真实云服务。范围是可执行的单段照片口播适配、原异步任务恢复和固定成片人工审片；不冒充完整云端分段生产、实时或3D数字人。

## 创作者故事与可测试验收

设计师复用已授权照片，明确同意将照片与录音发送到指定私有素材存储和百炼服务；导演看到实际原生窗口、查询状态和成片，逐项核对人物、口型、声音及AI标识，记录采用或具体返工意见。保存审片不调用模型、不自动发布、不覆盖时间轴。

- CR1 MUST：只有显式 `DIGITAL_HUMAN_PHOTO_PROVIDER=dashscope-s2v` 才选择专用云路线，固定模型wan2.2-s2v。普通video能力不等价。工作空间ID仅构造官方北京HTTPS地址，不接收用户自定义模型服务器或素材URL；无有效配置、许可声明、明确启用的私有桥接及精确空间授权时失败关闭。空provider沿用已有本地协议，错误云配置不退回本地或mock。
- CR2 MUST：API/worker均配置相同的workspace、密钥、分辨率、对象桥接和精确tenant IDs。调用者还须 `cloud_transfer_confirmed=true`、当前形象和独立声音授权；不接受通配授权。授权在素材上传、模型派发、查询、取回及发布前重新检查。临时素材URL和对象键只在私有检查点使用，API只展示恢复阶段。
- CR3 MUST：按实际协议提交image_url/audio_url，`X-DashScope-Async: enable`；原生照片边长400–7000、WAV/MP3音频不足20秒且小于15MB，480P/720P。仅适用于此供应商，不恢复项目素材/费用/次数限额。超窗明确拒绝，不自动截断、补循环或选择通用视频模型。长内容当前须在已有剪辑页准备短段，自动语义分段及跨段人物动作连续性尚未实现。
- CR4 MUST：先持久dispatching及来源/目的地摘要，再单次上传和POST；仅收到有效任务ID后持久remote_pending。之后只GET原任务ID，进程或查询失败可沿原resume核对，不重复提交。提交响应丢失、ID无效或只留dispatching时明确未知，禁止自动重发。已确认云结果下载通过现有SSRF保护并检查媒体后，复用engine_done/composed检查点、授权音轨合成和AI标识；后期恢复不重跑云模型。
- CR5 MUST：云目的地/分辨率/对象存储范围变更拒绝用新配置核对旧任务。每次取回对比固定来源摘要，素材或授权变化拒绝。取消只停止本地处理，不能保证停止云端计费；不以删除远端任务假装撤回推理。
- CR6 MUST：成片审片固定job+asset_id，用expected_version CAS追加不可变记录；采用必须四项人工确认，返工必须备注。记录审核人来自当前会话，不信任请求传入actor。权限撤销、跨空间/项目、CSRF缺失、非成功或不匹配成片均拒绝；新采用还检查授权未撤销。撤销后的历史记录仍可读，可记返工；不假装已撤回历史成片。
- CR7 MUST：网页审片具备原始形象/配音对照、有效视频预览、历史和当前意见、原请求丢响应刷新恢复。只读成员不可写。采用是创作者决定，不是平台自动质量认证或许可认证，不强加为全项目生成前置门槛。

## API与代码

`digital_human_cloud.go`专用HTTP适配及流式S3输入桥接，`digital_human_checkpoint.go`增加remote_pending、RemoteID、CloudDestination和输入对象键；所有敏感字段由publicCreativeJob在HTTP边界剥离。原CreativeJob增加cloud_transfer_confirmed和avatar_reviews；新任务仍用digital_human_authorized，不借通用video能力绕过授权。云任务ID不写通用UpstreamID，避免旧视频路径错误消费。

`POST projects/{id}/creative-jobs/{jobId}/avatar-reviews` 接受expected_version、asset_id、outcome(adopted/rework)、identity_confirmed、lip_sync_confirmed、audio_confirmed、disclosure_confirmed和note；回执返回id、version、reviewed_by、created_at及检查内容。同键同body返回原审片版本，即使之后已有新意见，也不替换原回执。历史随CreativeJob的文件/PG存储及既有备份迁移保留，不另建重复审片数据库。

`AvatarReviewPanel.tsx`复用现有editing-note、字段和原请求写入Hook；桌面对照双栏、手机单栏。数字人页明确云素材外传确认、原生窗口、查询原任务与后期恢复的差异。系统后端配置继续仅允许可信部署管理员，API key采用既有敏感值遮蔽保存，不提供给浏览器或执行器进程。

## 云配置与素材保留

```dotenv
DIGITAL_HUMAN_PHOTO_PROVIDER=dashscope-s2v
DIGITAL_HUMAN_CLOUD_WORKSPACE=<北京地域工作空间ID>
DIGITAL_HUMAN_CLOUD_API_KEY=<部署方私有密钥>
DIGITAL_HUMAN_CLOUD_TENANTS=<精确空间ID,逗号分隔>
DIGITAL_HUMAN_CLOUD_RESOLUTION=480P
DIGITAL_HUMAN_LICENSE_ACK=true
REFERENCE_BRIDGE_ENABLED=true
REFERENCE_S3_ENDPOINT=https://<部署方控制的S3兼容端点>
REFERENCE_S3_BUCKET=<私有bucket>
REFERENCE_S3_REGION=<region>
REFERENCE_S3_ACCESS_KEY=<私有凭据>
REFERENCE_S3_SECRET_KEY=<私有凭据>
```

配置示例不是实际凭据或许可证明，不能粘贴占位符后声称服务可用。默认关闭云路线，系统设置保存后需协调重启API/worker。

输入只流式上传至随机不可推测的job派生私有对象键，签名读取URL24小时有效（原任务结果/查询协议也只保留约24小时）。完成/供应商失败时尝试删除；网络/权限问题导致删除失败、进程崩溃或取消时不会保证即时删除。因此部署方必须为 `avatar-inputs/` 配置短期生命周期，并验证实际删除与数据合规；仅URL过期不等于对象删除。本项目不公开bucket，不向供应商发送存储凭据。此为受权外传，不代表供应商存储/删除政策已获法律验收。

## 兼容、恢复与迁移

原本地json-v1/files-v1保持可用，旧无云字段工作和摘要不变。新remote_pending状态及审片历史不得被旧writer覆盖；停所有writer、备份数据库与DATA_DIR后统一升级。无新数据库表；此前新增声音授权表仍须完成离线迁移。检查点原始/合成媒体须按现有blob迁移清单迁移。remote_pending可沿文件重启或PG租约恢复，只GET原任务；只有确认媒体才进入既有后期恢复链。

精确外部副作用边界：上传或模型返回与检查点落盘之间崩溃可能留下对象/未知远端计算。没有供应商幂等查询证据时禁止自动重试POST；不承诺恰好一次云端计费。派发后的授权撤销阻断后续新请求与本地发布，无法收回已传到供应商的资料。

## 未完成、外部依赖与下一步

- 单段专用云适配代码可本地测试；真实北京账号/API权限、该模型是否包含在Token Plan、网关是否透传本原生协议、对象存储可达性、商业许可和艺术效果均未验收。当前接入直达明确官方workspace地址，不支持把任意OpenAI网关URL当百炼原生协议；若部署要求仅经其网关，须明确该网关的原生转发合同和认证方式后另适配。
- 自动原生窗口分段/逐段声画连续性、完整take分支比较、跨段重做仍未实现。本轮人工审片和固定单段恢复不能冒充这些功能。
- 实时数字人未实现：需要选定实际双向实时驱动服务/本地模型，确认WebRTC/WSS合同、短期会话凭据、独立租户会话、语音中断/回声消除、授权撤销与断线恢复、并发GPU资源及许可。当前异步视频接口无法变成实时服务；不以普通聊天加视频播放器代替。
- 3D数字人未实现：需选定VRM/glTF等实际资产/骨骼约定、授权来源、BlendShape/音素/动作驱动、实际渲染器与输出合同；GPU、资产商业许可与真实动作测试必须单验。没有所选资产和驱动服务时不能凭通用LLM或静态3D预览宣称完成。

## 来源与验证策略

官方协议仅取 [wan2.2-s2v API](https://www.alibabacloud.com/help/en/model-studio/wan-s2v-api)，2026-11-14核对，页面更新2026-10-27。纯HTTP本地合成供应商来自此合同，由Go httptest实际接收上传/POST/GET/DELETE及验证状态；真实本地PostgreSQL作为存储，不将数据库模拟成HTTP。仓库没有相关Postman simulation，环境未安装Postman CLI，未进行团队mock发现；沿用项目已有HTTP协议测试，未对真实服务发请求。详见validation.md最终记录。
