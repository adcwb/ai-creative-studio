# 配置归属、来源及迁移

依据当前 `.env.example`、部署模板和后端实际环境读取路径；未读取运行环境中的 secret 值。系统设置中个人区域与组织管理区域分开，普通成员不能变更全局安全设置。

| 配置 | 归属／界面 | 来源与优先级／重启 |
| --- | --- | --- |
| 语言、主题、导航收折 | 当前账户个人设置 | 浏览器账户键；未设置时语言继承匿名明确选择／浏览器语言，主题 forest；立即生效，不跨设备同步 |
| IMAGE/TEXT/VIDEO/SPEECH/ASR_MODEL 及供应商选择 | 当前组织网关模型及功能绑定 | 新维护统一到系统设置的组织运行配置；本次明确选择优先于功能绑定，缺少配置不静默付费回退；保存后新任务生效 |
| 上游地址、API Key、请求超时 | 组织 AI 网关 | 服务端租户加密、独立版本；超时默认 120 秒，5–600；新建默认录入 Key，编辑默认 keep。原环境供应商密钥不自动复制到数据库 |
| 输出 token、视频规格、JSON 模式 | 组织网关模型 | token 默认 3000，范围 1–6000；视频按已支持兼容协议允许值；服务端校验，新任务生效 |
| SPEECH_VOICE | 配音功能绑定的默认音色 | 组织绑定／明确请求覆盖；不从环境自动导入未知身份的音色 |
| 费用币种、单次预留上限、依据 | 组织模型配置；预算在组织费用账本 | 默认 USD，整数最小货币单位；不同币种独立，无自动换算；上限不是供应商账单，默认零拒绝真实执行 |
| 组织名称、成员、角色 | 组织与成员页面 | 服务端权限与持久化，撤销实时检查；不属于个人设置 |
| HTTP_ADDR、FRONTEND_ORIGIN、VITE_API_BASE_URL | 部署／构建环境 | 监听、SSO 回跳与前端 API 目标，不允许普通用户改成任意代理目标；重启／重建。既有 CORS 仍回显非空来源，FRONTEND_ORIGIN 不是 CORS 白名单 |
| AUTH_MODE、AUTH_ALLOW_REGISTRATION、AUTH_COOKIE_SECURE、AUTH_TRUSTED_PROXY_CIDRS、OIDC_* | 身份信任与平台部署 | 保留启动环境；不是租户管理员权限。变更需运维评审与重启 |
| MODEL_CONFIG_KEK_BASE64、SOCIAL_KEK_BASE64、BACKUP_ENCRYPTION_KEY_BASE64 | 部署密钥管理 | 不可经网页配置／查看；API 与 worker 使用一致模型 KEK。轮换用已有专用离线工具，不能任意换值导致旧数据不可读 |
| MODEL_GATEWAY_ALLOWED_HOSTS、EXECUTION_POLICY_FILE、GATEWAY_MODERATION_* | 全局网络／费用审核安全边界 | 保留部署授权；页面模型上限不能覆盖审核与网络拒绝 |
| DATABASE_URL、DATA_DIR、STORAGE_*、QUEUE_BACKEND、BLOB_*、REFERENCE_*、WORKER_CONCURRENCY、BACKUP_PG_LIBRARY_PATH | 存储、队列、对象桥接、worker、备份 | 需一致部署及迁移／恢复流程，不热搬到租户设置；按对应工具说明重启 |
| METRICS_ADDR、TRACE_* | 运维监控 | 可暴露网络和本地路径，不放个人／组织页面；重启进程 |
| SOCIAL_TIKTOK_*、SOCIAL_DOUYIN_* | 平台应用审批、OAuth 与公共域名验证 | 保留部署；页面仅进行已有授权流程，不能由用户勾选假定平台已批准 |
| FORCED_ALIGNMENT_*、LIP_SYNC_*、SOUND_EFFECT_*、DIGITAL_HUMAN_LICENSE_ACK、MEDIA_BACKEND | 受信任执行器、许可、媒体环境 | 可执行命令和资源能力属于部署信任；不要开放任意命令输入 |
| MUSIC_*、JIMENG_REQ_KEY 与历史 *_PROVIDER／*_API_KEY | 历史厂商专用执行兼容 | 不自动映射为通用网关协议，不迁移密钥。旧记录保留，旧编辑接口停止写入；不能宣称通用网关已实现全部原生能力 |
| POSTGRES_PASSWORD、MINIO_ROOT_* | 基础设施服务 | 仅部署模板，不是产品用户账户或组织偏好 |

模型网关、模型参数和音色已有组织 API，本轮将它们整合到系统设置而非重复新建全局配置表。个人导航偏好新增账户隔离键。其余环境项涉及安全、部署或执行器信任，没有适合安全迁入普通网页的新增全局开关；不为“全搬过来”放宽边界。

## 加密前提

页面只报告加密存储是否可用，不回显 master key。部署管理员应在受控 secret 管理系统中提供一个 base64 编码的 32 字节随机 KEK，通过 `MODEL_CONFIG_KEK_BASE64` 注入所有 API 与 worker，再重启这些进程。不要把值写入 Git、前端变量、浏览器 localStorage、聊天或测试报告。已有密文须使用原 key 或按轮换工具迁移；不能直接生成新 key 替换。本文不替用户配置真实凭据。

用户随后在组织网关中录入供应商 API Key；服务端仅返回 encrypted/not_configured/revoked 状态，编辑时保持现有凭据，明确选 set 才替换。保存连接与检查目录均不授权生成；正式模型调用仍需独立确认、预算预留与审核。
