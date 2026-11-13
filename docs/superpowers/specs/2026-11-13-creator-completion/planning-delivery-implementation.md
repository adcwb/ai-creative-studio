# CC3 / CC4：季集纲候选与整季交付

状态：本地实现与阶段验收；尚未推送。本阶段接续 `7288122`，未合入画布/数字人分支。本记录不能视为第6–10项全部完成。

## 创作者路径与可测试契约

1. 在系列页选择一季，展开编剧工作台，选择本季主线或某一集。创作者先核对这次来源，再写创作要求、选择真实网关文本模型并明确提交。复用 `script_generation` 功能绑定，不新增网关额度、成本或审核就绪门槛。
2. 来源固定该系列目录、这一季各集的当前标题/简介、当前故事圣经和这一季的正式节拍版本；保存全体故事头摘要以检测后来修改。**不声称包含全部事实/事件或代替既有连续性检查**，不把计划当作已发生的剧情，不发送其他季的正式节拍正文。季与集使用独立稳定范围；季规划需要至少有一集作为持久任务的归属项目，不凭空建立隐藏作品。
3. 真实文本响应只成为候选，要求完整的目标、开场、冲突、转折、钩子和取舍说明，并严格核对季/集ID。候选持久保存固定来源与原始模型建议；创作者的编辑不会修改原候选。历史每页20条，只读摘要，打开后才读取完整来源/结果。页大小是读取工作集，不是候选总量限制。
4. 创作者比较当前正式计划与候选，编辑后明确确认，才追加正式节拍版本。采纳不改剧本、镜头、素材、授权或发布状态。目标版本CAS、故事头、目录及相关项目简介在短事务/锁内检查，来源变化则拒绝覆盖。文件锁顺序为 core → directory → story，不在锁内重新进入目录读取助手；PG复用 story/series 事务锁及项目行锁。原请求回放在新鲜度检查前返回原版本，保护之后的编辑。
5. 取消仅停止本地等待，不声称撤销已发往网关的请求。发送后结果未知、超时、中断不自动重发；相同请求键与原body只能恢复原任务。通用 creative resume 不接受文本集纲。缺少模型、模型/权限变化、源版本变化、结构无效和结果未知分别保留明确失败，不回退模拟数据。
6. 在系列页展开“本季交付”，预览本季**全部 ready 剧集**的最新成功正式成片，静帧预演不参与。无剧集、任一集缺成片或文件不可读取均显示具体位置，禁止悄悄跳过未完成集。创作者确认清单与发布权利后，由浏览器常规下载流取得一个整季ZIP，不在前端下载为大Blob。
7. 下载必须匹配预览摘要。目录、项目版本或成片选择变化时在发送ZIP前返回409；可重新检查清单。服务端依次打开每集三份文件并流式写ZIP，包含 MP4、SRT、VTT；顶层清单附季集身份、固定时间轴、导出时系列关联、实际字节数和SHA256。传输取消、读取失败或传输中大小/修改时间变化会中断ZIP，不生成成功清单。文件须为常规文件，拒绝符号链接。

## 代码与接口

- `backend/internal/application/outline_candidates.go`：固定来源、真实网关适配器、校验、摘要分页、明确采纳。
- `provider_creative_store.go` / `execution_requests.go`：复用已有 `creative` 持久任务及队列；内部 `outline` 类型必须来自专用服务端来源快照，公共JSON不能伪造私有快照。执行治理看到完整固定输入；clone隔离候选来源和结果。
- `model_runtime.go`、`gateway_runtime.go`、`gateway_bailian.go`：OpenAI兼容 chat/responses 和百炼文本适配器提供受控 planner；保留当前租户、模型、功能绑定、输入窗口/原生计数和派发前来源检查。无新的供应商接口、真实调用或凭据。
- `backend/internal/persistence/storyrepo/{store,postgres}.go`：`SaveWithHeads` 在已有故事存储中追加，并在同一锁/事务检查来源头和本地采纳guard；原有 `Save` 语义保留。
- `backend/internal/application/{season_delivery,media_delivery}.go`：整季预览/流式ZIP、共同文件完整性与取消检查。
- `frontend/src/features/series/{OutlineCandidates,SeasonDelivery}.tsx`、`WritingWorkspace.tsx`、`pages/SeriesPage.tsx`：复用设计token/故事字段/模型选择器/原请求恢复；新候选生成表单在查看结果时收起，已有计划才使用双栏比较，移动端单列。普通信息不用警告色包裹整个编辑表单。
- `api/openapi.json`：3个集纲路径、4个操作（列表、源预览、创建、采纳）及2个季交付路径、候选/摘要/来源/交付模型。

```
GET  /api/v1/seasons/{id}/outline-candidates/source?space_id=&episode_id=
GET  /api/v1/seasons/{id}/outline-candidates?space_id=&episode_id=&cursor=
POST /api/v1/seasons/{id}/outline-candidates?space_id=&episode_id=
POST /api/v1/seasons/{id}/outline-candidates/{job_id}/adopt?space_id=&episode_id=
GET  /api/v1/seasons/{id}/delivery?space_id=
GET  /api/v1/seasons/{id}/delivery.zip?space_id=&selection_sha256=
```

写操作沿用会话认证、CSRF、可信Origin、当前空间权限和请求幂等键。`episode_id` 省略表示本季主线。候选详情复用已有 `GET /projects/{id}/creative-jobs/{jobId}`，服务端检查当前空间/项目归属；私有 RuntimeSelection 不进入该详情响应。

## 持久化与升级

没有新增候选数据库表：固定来源/结果是既有 `creative.json` / `studio_creative_jobs` 中可选类型字段，继续由原有事务队列和备份序列化保存；普通旧任务JSON形状保持兼容。新增角色与个人迁移的表结构**尚未实施**，不能据此声称有自定义RBAC。

部署时API与所有worker必须使用匹配版本；旧二进制不认识新 outline 任务，不能混跑消费。回退前应停止writer并保留匹配的数据库与DATA_DIR完整备份，不让旧worker消费新任务。文件执行中断保存失败及不确定语义，PG沿用粘性派发/unknown队列机制；不因重启自动重发文本。CC1的PG资产种类约束升级要求仍见资产实施记录。

整季交付是只读即时请求与固定导出选择，**不是新的持久发布审批或商业授权记录**。已下载包不随作品后来编辑而改变；重新下载使用新清单。文件SHA只证明包内实际字节，不证明原始模型输出品质、存储从未被运营者篡改、法律许可或已发布。

## 验收记录

- 文件 + 真实本地PG：集纲真实合成HTTP请求、来源与目录字段、候选不自动入正式资料、人工修改/确认、来源CAS、原采纳重放保留后来编辑、跨空间拒绝、当前执行权限撤销、错误模型范围、模型删除后的原任务回放、文件重新打开后的候选持久性。
- 独立PG API/worker实例：既有creative队列执行新的outline任务一次，回读保存结果及同键不重复派发。
- 发出HTTP后主动取消：结果为 `execution_outcome_unknown`；相同键只返回原失败记录，resume拒绝，重新打开文件存储保留未知结果。没有用“重试一次”掩盖不确定调用。
- 文件 + PG整季：两集完整ZIP、三种文件/集、清单哈希、只读、租户隔离、未完成/缺失文件/更换最新成片/缺摘要拒绝、取消；101集目录跨页不丢集，文件中途变更及符号链接拒绝。
- 相关全包 `go test ./... -run 'Story|Creative|Gateway|Outline|SeasonDelivery|DeliveryPackage|WritingBeats' -count=1`，带真实PG DSN：通过，application 25.346s。不是不加筛选的全PG测试。
- 新集纲/交付专项 `-race`，带真实PG：通过，application 12.334s；含独立worker、未知不重发与文件变化校验。新增的季范围/101集用例也在相关回归执行。
- 前端类型、Lint、24文件78单测、生产构建通过；OpenAPI验证和i18n静态检查通过（本轮记录时2971条，最终以检查输出为准）。
- 真实安全登录浏览器：集纲生成丢响应/页面重载原请求恢复、人工编辑/采纳丢响应、随后编辑后重放不覆盖；整季两集实际本地FFmpeg渲染后下载、缺集反馈/显式确认/后来新增集409。1920×1080和iPhone 13均通过；此为本地合成供应商与素材，不是真实付费模型验收。
- 截图 `/workspace/artifacts/creator-completion/{outline-candidates,season-delivery}-{desktop,mobile}.png`；日志 `/tmp/creator-planning-{browser,regression,race-final}.log`。已实看桌面与移动端，按检查结果调整收起新建表单及空计划单栏。

后端不加筛选的完整文件模式 `go test ./... -count=1` 通过（application 165.528s，PG专项由上面的带DSN检查补充，并非全PG回归）。最终UI/构建复验见下方追加记录；未运行的Windows原生、全PG、容器/云端CI、真实S3或付费模型不记为成功。

## 明确尚未完成

自定义角色、个人作品迁移、所有权转移/解散、统一跨脚本/分镜/素材影响与整项目原子回滚、版本化封面/标题/简介/标签与跨平台交付清单尚未因本次代码自动完成。CC3一次准备一个范围的候选，支持人工反复比较，不是跨集自动批产或完整节奏评分器。CC4打包现有正式导出，不替创作者完成缺失成片，也不提供断点续传下载或持久打包队列。画布与数字人仍独立保留，尚未合入/推送。集纲适配器继续使用既有结构化JSON实现（单次编码请求8 MiB、响应1 MiB的实现内存边界），上游原生输入窗口另行校验；这不限制项目/资料/集数总量，但也不能声称适配器支持任意大小的单次上下文。商业订阅/支付、可信邮件送达、真实连续5–10集作品质量/许可/原生分辨率、真实外部发布均未验收，不能把这些外部前提和未编码功能混为一谈。

### 最终复验追加

- 后端完整文件模式全部包通过，application 165.528s；专项真实PG race通过12.334s，包含101集、季/集独立范围及独立worker。Go vet通过。
- Linux服务构建及Windows amd64 server/worker交叉编译通过；**没有在本机运行Windows原生验收**。
- 最终集纲与整季交付浏览器：desktop 2/2、mobile 2/2通过；截图已实看并复制到上述 artifacts 目录，另有 `outline-candidates-review-{desktop,mobile}.png` 局部视图。最终日志 `/tmp/creator-planning-browser-final.log`。
- 前端最终typecheck/Lint/build通过（Vite 2.60s）；24文件78单测通过，i18n 2972条、OpenAPI验证通过。最终浏览器运行使用最新后端及前端源码；没有把合成供应商结果记作真实模型艺术效果。
- 快速离开页面/React开发模式取消读取时，既有目录/故事错误路径仍可能记录503日志；本轮实际可见界面与请求恢复断言通过，没有以这些日志声称外部存储/生产环境完成验收。未扩大为另一次诊断平台改造。

当前下一步是CC5自定义角色及CC6个人作品迁移，再逐项核对CC7，不需要用户重新授权。没有推送/部署/生产迁移或真实付费调用。完整阶段完成且复核远端后才按已获授权推送并跟进CI。
