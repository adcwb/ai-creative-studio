# Token Plan / wan2.7-image 拒绝请求调查

日期：2026-11-12；基线 `0e49adf5c329b9918c86ff87461acd56559b71e1`，fetch 后远端无新增提交。状态：Draft（本地合同核验已完成，生产根因待证）。分支 `fix/bailian-wan-image-contract`；暂停短剧工作树保留，不推送、不部署、不改生产配置、不创建真实凭据、不请求真实模型。

## 问题与验收

US-BI-1：创作者的 `wan2.7-image` 生成显示 `provider_rejected`，需要确定可操作的原因，避免无依据重试产生调用。

| ID | 合同与验收 | 结果 |
| --- | --- | --- |
| AC-BI-1 | Given 选择百炼厂商，When 生成，Then 使用原生同步路径、Bearer、JSON messages、单张参数和合法画幅；不转 OpenAI 或 mock | 两模型 × 四画幅以 loopback HTTP 和真实序列化核验 |
| AC-BI-2 | Given 原生错误响应，When 运行真实网关执行器，Then 正确映射状态并保存 HTTP 状态和 request_id，单次请求、不泄露错误回显和密钥、不跨空间披露 | HTTP 200 code、400/404/413/422、401/403、429、500 本地覆盖 |
| AC-BI-3 | Given 仍选通用兼容厂商，When 上游拒绝不存在的图像路由，Then 能复现相同 provider_rejected，而非假称模型本身不支持 | 本地复现 `/api/v1/images/generations` → 404 → provider_rejected |
| AC-BI-4 | Given 现场失败记录，When 有授权地读取只读诊断，Then 按实际状态和 request_id 关联上游原因 | **待现场数据**；当前环境只读生产访问被网络代理拦截 |

## 代码事实

- `backend/internal/application/gateway_runtime.go`：厂商字段决定实际适配器，模型 ID/网关名称不会推断厂商；旧的空厂商保持通用兼容行为。
- `gateway_bailian_image.go`：原生图像路径 `/services/aigc/multimodal-generation/generation`，请求为 `input.messages` / `parameters.size,n`，响应取 `output.choices[].message.content[].image`，再走安全下载器。
- `gateway_bailian.go` / `provider_google_media.go`：请求或响应 code 的拒绝被归并为安全标签；`provider_rejected` 不是供应商原始错误码。
- `gateway_transport.go` / `model_usage.go`：已保存 `http_status`、`request_id`、HTTP 次数；没有保存原始错误 message、Key 或提示词。`ModelUsagePanel.tsx` 当前表格没有展示完整诊断字段，授权 API 可读取现有记录。

## 官方合同核对（事实与推断分开）

2026-11-12 查阅的 [Token Plan 图像接入](https://help.aliyun.com/zh/model-studio/token-plan-image-generation) 明确支持 `wan2.7-image` / `wan2.7-image-pro`，使用 Token Plan 原生图像接口和套餐 Key。并非“该套餐只支持文本”。文档允许省略尺寸由模型选择，当前项目四种显式尺寸也符合 [Wan 2.7 图像参数](https://help.aliyun.com/zh/model-studio/wan-image-generation-and-editing-api-reference) 的面积与比例范围；不能据此保证真实账号权益或每次请求被接受。

已确认的可复现场景：错误厂商路由被上游拒绝，映射成相同标签。**尚未确认用户现场是否属于此场景**。其他可能为参数/输入、套餐或模型权限、地域/凭据、具体部署版本；需实际 HTTP 状态和供应商 code/message 区分，不能凭空选定根因。

## 修改与验证范围

- [x] 查最新仓库和现有技能入口，保留工作树；生产适配器和生产模型设置未修改。
- [x] 新增 `gateway_bailian_diagnosis_test.go`，核验上表本地 HTTP 场景，沿用现有 httptest 设施；没有 Postman simulation 资源可复用，不引入新的模拟平台。
- [x] 补充专题使用文档的只读排查步骤；不增加产品额度、自动重试或静默协议回退。
- [ ] 获取现场失败记录，确认真正根因后决定是否需要适配器修复。当前未确认图像生成协议缺陷，未对生产逻辑作猜测性改动。

本地测试命令：`go -C backend test -count=1 -race ./internal/application -run '^(TestBailian|TestGatewayVendorPersistenceAndCompatibility)'`。最终完整目标套件通过（application 3.689 秒），含最后新增的错误厂商路由复现；`go vet ./internal/application` 与格式/差异检查通过。运行时及前端未修改，未重复执行无关全量创作或生产验收。

## 诊断阻碍与下一步

实际尝试只读 `https://ai.985898.xyz/healthz` 和 `/api/v1/auth/session`，执行环境网络代理返回 `Tunnel connection failed: 403 Forbidden`；网页工具也无法访问该站点。这个 403 来自诊断网络，**不是百炼模型的 HTTP 状态**。本环境没有可用生产会话、生产日志或浏览器会话工具，未访问生产凭据文件，未绕过认证。

已请求仅提供失败记录的 HTTP 状态、request_id、网关厂商及无密钥服务地址；如控制台已有供应商 code/message，提供脱敏说明即可。该只读信息是确认 AC-BI-4 的剩余依赖。无须真实生成、改配置或反复重试。
