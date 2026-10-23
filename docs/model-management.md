# 模型接入管理：配置与执行边界

租户管理员在页面维护真实持久化配置：协议适配器、固定区域、模型ID、能力、生命周期、启停及每种能力的默认项。管理配置保存到 `DATA_DIR/model-config.json`，与全局运行环境配置分离。平台级适配器仍仅由部署操作者配置环境；租户不能通过管理API修改平台运行配置。

**保存不是启用付费调用。** 第八轮已增加租户配置运行时：接收任务时显式选择配置ID与版本，服务端绑定认证actor和租户；worker在治理许可后才解密并构造固定官方端点adapter。配置默认项不会自动把现有mock任务改成付费请求。`runtime_applied`由当前治理就绪回调动态计算，表示运行时机制已接线且治理基础就绪，不表示某次任务已获得费用许可；`live_enabled=false`和独立live验证拒绝保持不变。没有执行真实模型调用，管理页面本地校验不能冒充账号权限、模型可用性或效果验证。

## 授权和协议

- `GET /api/v1/model-config?space_id=…` 和 `PUT` 仅该空间owner/admin可用，editor/viewer拒绝读取管理员接入配置；查询不是其他租户或平台配置的入口。
- PUT要求 `expected_version`，以及entry和credential对象。每租户整体版本CAS；新建entry不传id，更新必须引用同租户已有id。最多100条配置，默认项按能力唯一。
- entry为 `{id?, provider, model, capability, region, enabled, is_default, lifecycle}`。lifecycle仅active/deprecated/disabled；默认项须启用且active。
- endpoint、protocol由服务端已实现适配器和区域表生成，客户端不能传入任意URL、文件路径、内网地址或代理。旧OpenAI视频及未提供的即梦视频不在可选择列表。模型ID可配置不等于该模型仍可用，返回 `model_verified=false`。
- `POST /api/v1/model-config/validate?space_id=…` 接受 `{id}`，只检查本地结构和凭据密文可解密性，不读取引用环境变量，不联网。结果中的configuration_valid表示元数据结构；credential_status可另指示未配置、引用未验证、密钥缺失或密文不可读。
- `POST /api/v1/model-config/live` 固定403 `live_not_authorized`，不会调用供应商。

## 凭据管理

credential.action有keep/set/reference/revoke。keep不能附带secret/reference；若已有凭据，不允许更换provider后继续keep，避免把某厂商secret发送给另一厂商。reference只能选择当前适配器声明的凭据环境变量名称，返回 `reference_unverified`，不读取现有值、不证明部署配置存在。即梦AK/SK由两个独立环境变量组成，选中一个引用不代表凭据对已完整配置。

set只有部署明确注入 `MODEL_CONFIG_KEK_BASE64` 才可使用：其解码值必须为32字节随机密钥，无默认密钥、无硬编码或明文回退。输入secret仅内存处理，以AES-256-GCM随机nonce加密，AAD绑定租户ID、记录ID和provider。GET/PUT响应永不返回secret或ciphertext，仅返回secret_status、掩码和允许的引用名称。密钥缺失时503拒绝写secret，原有密文仍保留；错误KEK的本地验证返回credential_unreadable。更换KEK不是自动轮换，必须由操作者先做完整备份和受控重新录入；当前无密钥轮换迁移命令。

revoke原子清除当前密文/引用并禁用配置、撤销默认项。旧版本写入409，元数据keep不能恢复被撤销凭据。显式新版本set才可重新存入凭据。历史备份仍可能含旧密文，撤销不等于供应商端吊销，也不等于磁盘安全擦除；服务不会代替用户修改供应商权限。

## 备份、日志与验证

model-config.json为0600，临时文件写入、fsync、rename及目录fsync，加载缺字段/非法状态拒绝启动；依赖现有DATA_DIR单进程锁。完整备份需包含该文件；解密依赖独立保护的部署KEK，不能将KEK放入同一备份、源码或前端变量。恢复旧备份会恢复当时配置，应核对撤销记录后才使用。配置文件不存明文secret历史，审计只记可信身份/租户与请求分类，不记请求体、凭据或查询参数。

本地测试使用合成secret与临时KEK，覆盖跨租户读写、非管理员拒绝、CSRF、任意endpoint拒绝、密文绑定、错误KEK、无密钥拒绝、重启、版本冲突及撤销不能被旧请求复活。没有配置或读取用户已有secret值，没有执行供应商live调用。

## 第八轮运行时隔离

`ModelRuntime.Resolve`不读取secret，锁定当前租户配置版本；`Execute`必须获得执行治理的任务许可，随后解密AES-GCM凭据。每次HTTP发送、视频轮询和下载前重新检查组织成员角色、配置版本、能力与治理许可。配置变更取消本进程已注册的请求，成员角色变更也取消；回调结束后旧adapter不能复用。已经发送给供应商的请求无法撤回，取消后仍需保留未知费用。跨进程变更除每次发送前复核，还每秒读取共享权威状态；复核超时两秒即取消，在本地将检查延迟限制为最多三秒。不能撤回供应商已经接受的请求，不承诺供应商端瞬时停算。

默认仅租户独立加密secret参与运行时，禁止租户通过引用平台变量名借用部署操作者的credential。外部secret-store使用可替换`ApprovedSecretResolver`：部署显式绑定tenant、配置ID、provider与reference到外部secret ID，并注入遵守context取消的fetch实现；没有默认环境变量解析器，缺绑定即拒绝，HTTP不能创建这种平台授权绑定。返回的secret只用于当前执行，不缓存进配置。语音必须显式提供voice。即梦凭据使用JSON对象`{"access_key":"…","secret_key":"…"}`一起加密保存或由明确绑定的外部secret-store返回，禁止把单个AK或SK当作完整凭据。需要另行核准账户资格的MiniMax music、已弃用OpenAI speech尚未开放这个管理运行时，非whisper-1的ASR不套用旧时间戳协议；这些配置在任务接收前拒绝，无自动供应商fallback。

合成fixture验证真实adapter发送所保存的model及租户secret、固定HTTPS目标、跨能力拒绝、过期版本、跨租户、缺治理许可、撤销在途取消及回调结束后复用拒绝。fixture不代表真实供应商验收。

运行时使用`RuntimeNoDispatchError`标识可证明没有进入任何HTTP transport的本地失败，治理层可据此释放预留。连接失败、请求取消和下载尝试后错误均不能使用这个证据，保守保留未知费用，不推测供应商实际计费。

已知上游任务恢复仍要求操作策略明确确认查询免费，且不新预留、不释放旧未知费用。运行时仅放行GET，以及原生即梦adapter的固定POST查询：`https://visual.volcengineapi.com/`、`CVSync2AsyncGetResult`、固定版本和完整查询body严格校验，task_id必须等于服务端持久任务的上游ID。视频GET的厂商、路径、query同样精确绑定原上游ID，不能用相同配置查询另一任务；创建动作、其他endpoint、其他req_key或额外body字段拒绝。POST查询没有伪装成通用可写授权。
