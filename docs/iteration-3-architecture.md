# 第三轮：固定 OpenAI 图像协议适配器

本轮以 `6e30ff6` 为基线，保留第二轮完整业务链路，只增加一个明确的外部图像生成协议适配器及其配置状态、结果存储、恢复与错误路径。默认仍为 `mock-svg`。本轮开发与验收不读取真实密钥、不发送真实付费请求，不推送、创建 PR 或部署。API 以 [OpenAPI 0.3.0](../api/openapi.json) 为准。

## 已核实的官方协议

2026-10-22 查阅官方文档后，固定选择 **`gpt-image-2-2026-04-21`**。模型页列出该 snapshot；图像生成参考允许该模型使用 16 倍数宽高的自定义尺寸。三种现有比例因此可以映射为精确画布尺寸，无需裁剪、拉伸或虚标比例。[官方模型页](https://developers.openai.com/api/docs/models/gpt-image-2)、[Images generations 参考](https://developers.openai.com/api/reference/resources/images/methods/generate)。

| 应用参数 | 上游固定映射 |
| --- | --- |
| endpoint | `POST https://api.openai.com/v1/images/generations` |
| `model` | `gpt-image-2-2026-04-21` |
| `ratio=1:1` | `size=1024x1024` |
| `ratio=4:5` | `size=1024x1280` |
| `ratio=16:9` | `size=1536x864` |
| 每任务产物数 | `n=1` |
| 质量与格式 | `quality=low`、`output_format=png`、`background=opaque` |
| 流式传输 | 不启用；`stream=false` 可显式传入或省略 |
| 应用 `kind` / `style` | 使用确定性的类型和风格说明编入上游 `prompt`，同时保留用户原描述 |
| 结果 | 单个 `data[0].b64_json`，解码后必须是合法且尺寸精确的 PNG |

GPT Image 默认返回 base64 图像；官方文档将 `response_format` 和独立 `style` 参数标为不支持 GPT Image，因此适配器不发送这两个字段。不能把 `natural/studio/illustration` 直接当作上游 `style` 枚举，也不能使用 URL 返回结果作为下载入口。[官方参数与返回结构](https://developers.openai.com/api/reference/resources/images/methods/generate)。

上游采用 HTTP Bearer 认证，凭据仅由服务端配置读取；浏览器、任务输入、状态文件与日志均不能包含密钥。[官方认证说明](https://developers.openai.com/api/reference/overview#authentication)。本轮不实现“OpenAI 兼容”泛接口、任意模型/endpoint、图像编辑、参考图上传、流式预览、透明背景或多图结果。

以上是文档核实及本项目选定的协议子集；**没有通过真实付费调用验证账号权限、配额、生成质量或延迟**。PNG 字节、HTTP 响应和解码限制是本项目的资源策略，不是对官方最大能力的声明。

## 服务端配置与安全能力状态

生产入口只读取 `IMAGE_PROVIDER` 与所需的 `OPENAI_API_KEY`。`IMAGE_PROVIDER` 缺省为 `mock-svg`，只接受 `mock-svg` 或 `openai-images`；默认 mock 分支不需要读取外部服务密钥。OpenAI endpoint 和模型版本为代码中固定的已验证协议，不提供 base URL 配置、浏览器凭据输入或创建请求内的 provider/model 字段。

配置在进程启动时确定，无运行时切换 API。未知 provider、缺失密钥或不安全配置不会静默回退 mock，服务仍可启动、读取旧任务和素材，但拒绝接受新的任务。凭据是否存在只表示本地配置就绪；不发送联网探测请求，不声称通过了远端认证。

新增 `GET /api/v1/capabilities`，无需 `space_id`，返回：

```json
{
  "provider": "openai-images",
  "mode": "real",
  "ready": false,
  "model": "gpt-image-2-2026-04-21",
  "reason_code": "missing_api_key",
  "message": "外部图像服务未配置，暂不能创建任务；历史记录仍可查看。"
}
```

| 字段 | 合同 |
| --- | --- |
| `provider` | `mock-svg` / `openai-images` / `unavailable`；未知配置用安全值 `unavailable`，不回显原字符串 |
| `mode` | `mock` / `real` / `unavailable` |
| `ready` | 本地校验是否允许接受新任务；不是远端认证、余额、配额或健康探测结果 |
| `model` | OpenAI 模式下返回固定 snapshot；mock 省略 |
| `reason_code` | 未就绪时为 `missing_api_key` / `invalid_provider` / `invalid_configuration`，就绪时省略 |
| `message` | 静态安全提示，不含配置原值、密钥、内部路径、上游响应或请求 header |

能力接口本身在 `ready=false` 时仍返回 200。创建/重试的新提交返回 503 `provider_not_ready`，不入队、不访问上游。原任务列表、详情、结果、素材读取与已有成功任务的保存继续可用。`GET /healthz` 仍表示演示服务进程可响应，不代替该能力状态或外部依赖健康度。

模板及可选比例/风格仍由原 `GET /templates` 提供；两种 provider 都支持本轮三种比例，未增加无实际用途的重复选项目录。

## 任务绑定与业务幂等

普通创建和失败重试均在首次被接受时固化实际 `provider` 与可选 `model`。业务输入仍只有 `prompt/kind/ratio/style`；HTTP 请求另可携带成对的 `expected_provider/expected_model` 前置条件，用于防止界面看到的模式与服务端实际模式不一致。它们不能改变服务配置，也不进入持久化 Input 或幂等指纹。调用者不能指定 endpoint、密钥、实际生成模型或结果路径。重试仍仅允许同空间 `failed` 父任务，创建新 ID 并持久化直接 `retry_of`，不修改父记录。

浏览器必须始终提交当前能力状态对应的完整前置条件：mock 为 `expected_provider=mock-svg`、`expected_model=""`；外部模式为 `expected_provider=openai-images`、`expected_model=gpt-image-2-2026-04-21`。两项缺一、显式 null、未知值或无效组合返回 400。旧 API 客户端两项均省略仍兼容原行为，此时由该调用者负责确认当前模式和费用边界。

幂等比较继续是空间内的 `(key, normalized input, retry_of)`，**不把当前配置当成旧请求的身份**。在输入与父任务权限校验后，先检查已有幂等记录，再对新的提交检查当前 `ready` 与期望模式；检查和创建在同一仓库锁内完成。新提交在 ready=true 但预期 provider/model 不符时返回 409 `provider_changed`，没有新任务或上游调用。不能仅靠提交前 GET 能力状态代替这个原子检查，因为 GET 与 POST 之间仍可能发生进程重启和模式变化。

同键、同输入、同父的重放返回原任务及其原 provider/model，即使当前进程已经切换模式、缺少密钥或配置无效；有效但已过时的 expected 对不会阻挡该重放。相同键换输入、换父或混用普通创建与重试仍为 409。

| 场景 | 行为 |
| --- | --- |
| mock 创建成功，重启为 OpenAI 后重放同次提交 | 200 返回原 mock 任务，不生成 OpenAI 任务 |
| OpenAI 请求已接受，重启后配置不可用，同键重放 | 200 返回原任务及恢复后的实际状态，不因 ready=false 强行创建或改绑 |
| 配置不可用时用新键提交 | 503，无新任务或上游调用 |
| 浏览器停留 mock 模式，服务端重启为可用 OpenAI，新键带旧 mock 前置条件提交 | 409 `provider_changed`，无新任务或上游调用；刷新模式后由用户再次明确提交 |
| 明确发起新的失败重试 | 使用新键，绑定当前 provider/model，原父保留；当前不可用则 503 |

前端网络结果未知时保留原提交键、完整内容和父任务，能力状态变化不能清除该键。能力加载失败或 ready=false 时，前端禁止所有创建/重试 POST；不提供“确认上次提交”的特殊 POST，因为原键可能尚未被服务器接受，实际服务若已切换到外部模式便可能产生新任务。能力成功恢复 ready 后，按当前模式清晰提示，再由用户明确点击提交并沿用原业务键。`provider_changed` 只触发能力刷新和说明，不能自动重发。前端幂等指纹也应排除 expected 字段，避免刷新模式时换掉未知请求的键。

成本提示以本次实际选中的 provider 为准：历史 OpenAI 失败但当前 mock，说明本次是模拟；历史 mock 失败但当前 OpenAI，说明新尝试可能产生费用。对历史外部超时或中断，另提示上一次远端结果/计费可能不确定。界面不能用当前模式给历史任务和素材重新贴来源标签。

## Eino 与有界执行

保留实际 Eino `Compile` 与 `Invoke`，将 provider 返回值从 SVG 字符串扩展为结构化产物，例如 `GenerationResult{Data, MIMEType, Width, Height}`。Normalize 节点继续校验输入，provider 节点调用 mock 渲染或固定 OpenAI HTTP adapter。仅引入本轮实际使用的接口能力，不创建空模型、数据库或对象存储模块。

worker 必须先将任务从 queued 原子提交为 running，再调用 provider。单 worker、阶段进度 `0 → 35 → 100` 和最终 `succeeded/failed` 状态保持；进度不是外部模型生成百分比。HTTP 请求使用任务执行 context，整体超时固定 120 秒；测试可通过构造器注入短期限。

上游请求不自动重试，不跟随任何 HTTP redirect。固定官方地址只能通过服务端代码使用；不访问响应中的 URL。即使业务提交幂等成立，也不能声称外部生成 exactly-once。官方协议文档没有为本项目建立图像生成去重/计费恢复保证；超时后无自动查询或补偿调用。

## PNG 验证与安全落盘

只有通过下面验证的产物才能进入成功状态：

1. 读取上游响应时限制整个响应体最多 **24 MiB**，包括 JSON 与 base64；不能依赖 `Content-Length`，也不能在完整无界读取之后才检查长度。
2. 成功响应必须只有一个图像对象，含非空 `b64_json`。URL-only、空数组、多图、无效 JSON/base64 和不受支持的媒体格式均失败，不下载 URL。
3. 解码字节最多 **16 MiB**。先用 PNG `DecodeConfig` 验证实际宽高严格等于本任务映射尺寸，像素总量不超过 **2,000,000**；先检查尺寸再完整解码，避免按不可信宽高分配巨量内存。
4. 完整 PNG 解码必须成功。仅凭 MIME header、文件扩展名、PNG 签名或图片头不能认定有效。实际输出 MIME 固定为 `image/png`，宽高来自验证结果。
5. 图片保存在 `DATA_DIR/results` 的受限根目录内，名称只能由服务端内部随机任务 ID 派生，不采纳请求、prompt、上游 URL 或 response 中的文件名。
6. 写入权限为 0600 的临时文件，fsync 后 rename 到固定结果名，再将结果引用和元数据与任务成功状态一起提交到 JSON 快照。快照失败时不能向用户返回 succeeded；孤立文件可保留但不可被未成功任务读取。

使用 Go 1.25 的受限目录句柄 `os.OpenRoot` 或等价的严格边界措施，确保读写不会通过路径或符号链接逃逸结果根目录。对 PNG 结果，持久化路径也必须与内部任务 ID 的固定文件名一致；不能把 `state.json` 中任意字符串直接拼接成可读路径。产物内容、base64、凭据和上游响应体不写入日志。

`GET /tasks/{id}/result?space_id=…` 先通过仓库校验空间归属与成功状态，再读取受限路径或旧内联 SVG。根据实际结果返回 `image/png` 或 `image/svg+xml`；保留 `nosniff` 和 `no-store`，SVG 保留 sandbox CSP。结果文件缺失、损坏或不安全引用返回受控的存储错误，不回显本地路径，也不自动调用 provider 重新生成。

旧 SVG 保持原样、内联保存，不为了切换 provider 重绘或迁移成 PNG。新 Task/Asset 增加可选 `model`、`result_mime`、`result_width`、`result_height`；新成功产物记录实际媒体类型与尺寸，旧成功 SVG 补 `result_mime=image/svg+xml`，历史尺寸可以省略，不猜测。素材元数据从成功任务复制，不接受客户端伪造。

## 错误与恢复

失败任务保留面向用户的安全 `error`，新增可选 `error_code`。错误类别和静态文案由本地代码控制，不把 provider/HTTP 错误的原始 `Error()`、响应 body、header、URL 或 token 透传进 JSON、状态文件和日志。上游错误中的未知文本仅可丢弃，不能作为用户可见说明。

| `error_code` | 含义与处理 |
| --- | --- |
| `mock_failed` | 模拟 provider 明确失败；不会被当作上游错误 |
| `provider_auth_failed` | 上游 401/403；提示检查服务配置，不泄露凭据 |
| `provider_rate_limited` | 上游 429；本轮不自动等待重试 |
| `provider_rejected` | 其他上游 4xx；返回静态失败说明 |
| `provider_error` | 上游 5xx 或传输错误；不自动重试，某些传输失败的远端结果可能不确定 |
| `provider_timeout` | 超时；上游可能仍在处理或已经计费，需明确决定是否新尝试 |
| `invalid_provider_response` | 200 响应仍不符合 JSON/base64/PNG/尺寸/字节限制；不会保存为成功，上游可能已产生费用 |
| `execution_interrupted` | 外部任务执行期间停服/崩溃后恢复，远端结果或费用未知，不自动再次发送 |
| `provider_unavailable` | 持久化任务绑定的 provider/model 当前不可执行，或当前配置不可用；不发起上游调用 |

API 级 `provider_not_ready` 表示新请求没有被接受，与已经持久化任务的 `provider_unavailable` 区分。`error_code` 在旧失败记录中可以缺失；原任务错误不覆盖成当前服务模式的消息。

恢复继续读取 `state.json` version 1 并保留旧 ID、输入、幂等键、空间、SVG、素材和重试关系。新增字段缺失按旧 mock 数据兼容，不把历史任务迁移成外部 provider。

已有快照必须明确包含受支持的 `version`、非 null 的 `tasks` 与 `assets`。缺失或损坏时拒绝启动并保留原文件，不能套用新库的空映射默认值后覆盖，以免丢失幂等记录并导致原请求再次执行。只有快照文件不存在时才初始化新库。

| 恢复时状态 | 策略 |
| --- | --- |
| mock `running` | 保持原行为：恢复 `queued/0`，由与绑定一致的 mock provider 处理 |
| OpenAI `running` | 恢复 `failed/100`、`execution_interrupted`；即使网络请求尚未发送也保守处理，不自动再次付费调用 |
| 任一 `queued`，provider/model 与当前 active 一致且 ready | 按已有绑定执行；不能替换成新的 provider/model |
| `queued` 与 active 不一致或当前不可用 | 安全结束为 failed/provider_unavailable，不访问上游，不无限卡在队列 |
| `succeeded` / `failed` | 保留终态，不重放；历史结果读取不依赖当前配置就绪 |

文件仓库的锁、快照提交和单写者限制不变。图片 rename 与快照 commit 之间没有数据库事务：发生崩溃可遗留不被引用的文件；不保证整机断电下数据库级持久性，也不把孤立 PNG 的存在当作远端请求可安全重放的依据。本轮不新增费用对账、后台重试、租约、分布式队列或自动孤立文件回收。

## 无付费测试边界

生产入口只有固定官方 endpoint，不提供任意或 loopback base URL 配置。测试代码可在 adapter 构造器中注入本地 `httptest` 服务地址和 HTTP client；注入属于测试依赖，不改变生产模式。浏览器 PNG 联调如需要独立进程，可由仅 `_test.go` 的测试 harness 启动真实 handler、worker、临时文件仓库和本地假上游，不能为此新增生产测试开关。

所有测试使用合成固定密钥和合成 PNG fixture，不读取开发者真实凭据。假服务验证真实 HTTP 方法、路径、认证 header、固定模型参数、比例尺寸和 prompt 映射；返回可复现的成功或错误 payload。浏览器和截图中的这种图片须标为协议测试样本，不能作为真实 OpenAI 画质、账号权限或性能的证据。

最小验收证据：

1. 默认无密钥运行 mock 全流程；缺密钥/非法配置真实后端返回安全能力状态，新的创建/重试 503，历史读取与同键已接受请求重放保持。
2. 本地 HTTP fake 经实际 adapter、Eino、worker、PNG 校验、持久化、详情、保存素材与结果读取完成成功链路，重启后仍可读取。
3. 验证三种 size 和 kind/style 提示词映射，拒绝 URL-only、redirect、多图、超长响应、坏 base64、非 PNG、损坏 PNG、错尺寸及巨量像素头；不存在任何外部下载请求。
4. 验证 401/403/429/其他 4xx/5xx、超时、传输失败，确认无自动重试；恶意上游错误正文、header 和测试凭据不出现在 API、状态文件或日志中。
5. 在上游已接收请求后中断进程再恢复，不出现第二次自动上游调用；真实 running 变为 execution_interrupted，mock 恢复策略不回归。
6. 跨模式重启与配置不可用后重放同键仍返回原 provider；旧 queued 不被新 provider 接管；显式新 retry 才产生当前 provider 的新执行尝试。浏览器缓存 mock 模式时切换后端为 OpenAI，新提交必须被原子 expected 检查拦为 409，不能产生费用；前端刷新模式后仍需用户再次点击。
7. 结果空间隔离、固定路径与符号链接逃逸、文件缺失和落盘失败都有受控错误；失败任务不会保存成成功素材。
8. 前端准确展示当前能力、每条记录的实际来源、MIME/实际尺寸及失败重试成本提示；能力加载失败有恢复入口，布局验收由独立角色完成。

本轮未建立生产认证、组织成员授权、企业隔离、账单/费用保证或模型质量评估。两个演示空间仍是公开数据归属选择，CORS 不等于授权；配置真实 provider 不会自动使应用具备安全的公网部署条件。

## 架构角色的已完成核对

2026-10-22，包含 expected 前置条件的 OpenAPI JSON、本地引用、组件 schema 与 33 个示例通过校验。实际启动当前 Go 后端，使用独立临时数据目录和随机 loopback 端口，103 个 HTTP 响应（含状态轮询，并非 103 个独立用例）逐项符合对应响应 schema。

该次实际调用保留第二轮目录/创建/失败重试/保存/搜索/重启检查，并覆盖当前能力状态、mock 实际媒体元数据、创建及重试的 expected 校验 400/409、已有键先于模式/就绪检查的重放、三种不可用配置的新请求 503，以及这些配置下历史结果和素材仍可读取。运行只启用 mock 或明确不可用的外部配置，未开启任何可发送上游请求的生产配置；所有运行目录和进程在退出路径清理。

只读审查确认 worker 在调用前提交 running，执行时核对持久化 provider/model；外部 running 重启失败不重发，原 key 重放先于 ready/expected 检查。PNG 经过限定读取、精确尺寸与完整解码验证，结果路径使用受限根目录、固定内部 ID 和不跟随末端符号链接的读取方式，并以非阻塞打开后校验常规文件，防止损坏引用指向 FIFO 时阻塞。审查中发现的 base64 padding 长度上界误拒精确 16 MiB 产物问题已由实现角色修正。

最终审查还发现已有快照解码沿用新库默认值时，缺失顶层字段可能被误认为空库。实现现先解码到零值快照再校验；独立临时进程验证 `{}` 会拒启且原文件不变。QA 新增 7 个空对象、null、缺字段或 null 映射回归用例，均检查拒绝打开及字节保留；实现角色完成全量 `go test -race ./... -count=1 -timeout=60s` 与 `go vet ./...`，主集成另行复验 build 与 smoke 通过。

已核读独立 QA 的 [PNG 协议与浏览器检查](../scripts/provider_check.mjs) 及仅测试编译的 [harness](../backend/internal/application/provider_harness_test.go)。该次结果记录为 5 次明确的 loopback 假服务调用，覆盖 PNG 浏览器解码/保存/重启、缓存 mock 模式切换为外部配置时 guard 拦截且零调用、响应丢失后原键重放、能力未知时禁 POST、SIGKILL 后 execution_interrupted 且无自动重发，以及显式重试关联。采样结果的 `model/result_mime/result_width` 字段与本契约一致。这些图片是合成协议 fixture，不能作为真实模型生成、权限或费用验证证据。
