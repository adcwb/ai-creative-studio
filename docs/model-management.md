# 模型接入管理：配置与执行边界

租户管理员在页面维护真实持久化配置：协议适配器、固定区域、模型ID、能力、生命周期、启停及每种能力的默认项。管理配置保存到 `DATA_DIR/model-config.json`，与全局运行环境配置分离。平台级适配器仍仅由部署操作者配置环境；租户不能通过管理API修改平台运行配置。

**保存不是启用付费调用。** 当前管理响应固定 `runtime_applied=false`、`live_enabled=false`；配置尚未接入worker选择，现有运行时真实模型安全护栏继续生效。用户明确授权具体供应商、模型、数据与预算并完成运行时预留/结算治理前，不执行live验证。管理页面不能用本地配置校验冒充账号权限、模型可用性或效果验证。

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
