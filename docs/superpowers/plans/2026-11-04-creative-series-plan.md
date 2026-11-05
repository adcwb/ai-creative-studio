# 自定义提示词与长篇漫剧实施计划

创建：2026-11-04；修订9：2026-11-05。状态：Implementing，**P0A首批实现已开始，任务未全部完成**。已fetch核对代码基线与origin/master均为`9e687c584632fdc98628c42592ff3df2ff0304cb`。用户已授权按阶段实施；按最新授权，全部规格实施且验证完成后推送GitHub并跟进CI；完成前只做本地阶段提交，不部署。

规格：[入口](../specs/2026-11-04-creative-series/README.md) · [需求](../specs/2026-11-04-creative-series/requirements.md) · [设计](../specs/2026-11-04-creative-series/design.md) · [验收](../specs/2026-11-04-creative-series/acceptance.md)。以下目标路径是拟定实施位置；已存在的文件按最小改动扩展，不要求一次重写所有模块。

## 依赖与优先顺序

```mermaid
flowchart LR
  P0[明确规格与代码清单] --> P0A[现有路径去配额 流式存储 1K至4K后期]
  P0A --> P1[纯文本个人与组织模板]
  P1 --> P2[变量与参考配方]
  P0A --> P3[系列结构与剧作连续性]
  P2 --> P4[导演预演 依赖制作与返修]
  P3 --> P4
  P4 --> P5[容量运维与200集验收]
```

修订2优先完成当前入口去额度与60–120秒/1K–4K后期，避免等待长篇平台重构。随后先交付“像内置模板一样保存自己的提示词”；模板与系列可分别交付。200集是容量验收样本，不是用户上限；真实3集/20集样片属于另获授权的效果评估。

## P0：规格与基准准备

- [ ] **T01** 按已确认的60–120秒/集、用户选1K–4K、无项目配额锁定范围；明确输出档精确像素、画幅、P1字段/权限和其他未决创作取舍，不重复询问已确认要求。责任视角：产品/设计师/编剧/导演。依赖：无；退出：参数合同可验收，P3前解决表现形式/旧项目策略。
- [ ] **T02** 固定现有回归与容量基线，收集旧多集、旧模板、撤销成员、未知上游等合成夹具。目标：现有 e2e、`backend/internal/application/*_test.go`、性能脚本目录按仓库惯例确定。依赖：T01对应假设；退出：记录当前scope查询、限额和迁移夹具，不调用付费服务。对应 AC-S02、AC-O02、AC-SCALE。

## P0A：当前创作路径去配额与高清后期（新增最高优先级）

- [ ] **T03** 按research的L01–L16形成生产调用/所有配额清单并实体化热点写路径：media/creative/alignment/usage/library/review/resume/social/digital_human，补分页、紧凑回执及文件分片存储；同步评估网关目录和相邻账户条数门槛。依赖：T01–T02；目标：现有store/repo、`postgres.go`、`api/openapi.json`和迁移器。验收AC-Q01、AC-Q03、AC-Q05、AC-O07；退出：不存在用满500/1000/10000或全量快照溢出而拒绝后续调用的隐蔽路径，历史/unknown可查询。
- [ ] **T04** 删除累计/小时/容量业务判断，拆开active与queued，持久接受后排队；重点删除`guardCreativeLocked`全局20次、alignment的20/10次、库/媒体/回执/审片/发布历史上限及2GiB累计，保留鉴权/审核/幂等。旧预算方法标历史用途，非财务receipt独立存储且兼容旧ID。依赖：T03；验收AC-Q01–AC-Q05、AC-O01–AC-O05；退出：旧边界回归通过、无预算/自设额度调用图，未持久化绝不202。
- [ ] **T05** 版本化OutputProfile/GenerationSpec/native能力与精确像素；新增1440p/1800p、旧清晰度别名兼容。改Blob流式Put/OpenRange、分块上传、BIGINT size约束与health/backup/migration，修旋转与竖屏4K校验。目标：`modules/media/timeline.go`、`gateway_models.go`/适配器、`blob_store.go`、`blob_manifest.go`、`media_http.go`、`api/openapi.json`。依赖：T03；验收AC-R01–AC-R02、AC-R04；退出：native与output分开、旧产物不重编码、合法大于128MiB对象全链路可保存/读取。
- [x] **T06** 实现RenderManifest/Segment/Artifact、全局帧/采样时码、有handle的跨段转场、字幕/叠层/混音和检查点；移除完整时间轴的30镜/8轨/4层及分辨率时长耦合，删除-fs截断，采用每段资源调度/心跳。目标：`media_render.go`、`render_segments.go`、`render_checkpoint.go`、`queued_export_worker.go`、worker、`MediaStudio.tsx`/`mediaTimeline.ts`与i18n。依赖：T04–T05；验收AC-R01、AC-R03–AC-R05、AC-Q02–AC-Q03；退出：4K120秒可完整计划和执行，取消/资源不足不发布残片，用户不遇到15秒/128MiB或2个queued就拒绝。
- [ ] **T07** 完整P0A回归：旧额度边界、真实FFmpeg60/90/120秒及1K–4K、竖屏2160×3840、有效大文件/Range、本地/S3/PG/文件存储、备份恢复与故障注入；桌面手机说明native/升采样/等待状态。依赖：T04–T06；验收全部AC-Q/AC-R及当前图像/短剧权限与无mock回退。退出：精确列出通过/失败/未执行，不用短样本替代完整4K验收；不调用付费模型。

当前执行状态：T03–T05/T07为部分推进，T06本地渲染退出条件已验证；已移除数量/小时门槛并落地流式高清后期，文件资产库已按实体追加事务去除64MiB累计快照瓶颈，但其他领域整体scope、原生能力协商等尚未完成，渲染核心及最终回归见渲染记录；S3原生分段恢复与清理已按S3记录交付；可恢复UploadSession已按上传记录交付。T11–T14的纯文本闭环已推进，见提示词实施记录；T21变量编译及预览、T22/T23参考配方与项目真实网关集成已完成，长剧尚未完成。用户明确要求优先交付自定义提示词，独立实体仓库不依赖旧scope重构，因此P1与剩余P0A并行推进，不把T07未完成误报为依赖已通过。

## P1：自定义纯文本提示词

- [x] **T11** 实现prompts纯领域、plain配方版本和可见性/归档/复制规则，不引入模板总数配额。目标：`backend/internal/modules/prompts`。依赖：T01对应P1字段及已验证的生成入口；不等待与提示词无关的P0A渲染检查点；验收AC-P01–AC-P03、AC-Q01。退出：不可变版本、private/organization权限规则、内置只读合同确定。
- [x] **T12** 实现按空间/用户查询的promptrepo、PG/文件存储、迁移与HTTP接口；元数据/版本/回执事务原子。扩展现有tasks的可选来源快照，不修改旧templates响应。目标：`persistence/promptrepo`、`application/prompt_*`、实际OpenAPI文件、离线迁移/备份注册。依赖：T11；验收 AC-P02、AC-P03、AC-P05、AC-P07；退出：双API并发与角色矩阵通过，无新整体scope。
- [x] **T13** 模板区域“内置/我的/组织”、保存抽屉、搜索标签收藏、版本/归档、草稿保护与共享提示；纯文本选用只预填。目标：`TemplateGallery.tsx`、`Composer.tsx`、`hooks/useWorkspace.ts`、`features/prompts`、API与中英文本。依赖：T12合同；验收 AC-P01、AC-P02、AC-P07、AC-P08。退出：桌面/手机/键盘可用，用户正文不走翻译key，零隐式模型调用。
- [x] **T14** 汇总P1迁移/文档与回归。依赖：T12–T13；验收 AC-P01–AC-P03、AC-P05、AC-P07–AC-P08。退出：后端race/file/PG、前端检查、HTTP/OpenAPI/安全合同与相关浏览器测试通过；旧图像入口无回退/成本门槛回归通过，记录未测项。

## P2：变量、来源与参考配方

- [x] **T21** 添加variables_v1编译器和字段级错误，展开前后Unicode校验、plain兼容、预览摘要。目标：prompts模块与API schema。依赖：T14；验收 AC-P04。退出：禁止表达式/网络、缺失/未知/超长和模糊输入覆盖，无静默截断。
- [x] **T22** 提示词版本引用现有资产版本、参考归属/用途、有效输入来源与edited摘要；增加“在项目制作中使用”入口。目标：`library_binding.go`、`provider_creative_store.go`、相关model/input、前端配方/项目引用编辑器。依赖：T21；验收 AC-P05–AC-P07。退出：不支持的入口/适配器拒绝，不无声丢引用；私有模板进入共享任务有明确提示。
- [x] **T23** 合成供应商验证实际请求、参数覆盖、引用顺序、来源重试、上游错误与无成本配置。依赖：T22；验收 AC-P04–AC-P08、AC-O01。退出：权限/审核/实际能力与新版模板不冲突，无付费请求、无mock回退，更新使用文档。

## P3：系列结构、独立剧集与编剧工作台

- [x] **T31** 实现series/season/episode实体、分页仓储、同事务创建每集Project及独立版本，UI集号/导出profile快照适配，不设200集产品上限。目标：`modules/series`、`persistence/seriesrepo`、`application/series_*`、`features/series`、`ProjectPage.tsx`。依赖：T01表现/迁移决策、T02、T07；验收AC-S01、AC-Q01、AC-R01。退出：200集及新增201集元数据可管理，不声称已通过完整生产容量。
- [ ] **T32** 稳定character/scene ID和旧索引/名字映射；旧多集显式拆分预览/迁移、素材授权复制、幂等中断续跑与旧入口兼容。目标：`modules/projects/script.go`、`storyboard.go`、`media_episodes.go`、Project/Media仓储、离线迁移器。依赖：T31；验收 AC-S02、AC-S03、AC-O08。退出：对账与回退演练通过，不能猜测未知归属。
- [ ] **T33** 正式圣经、角色弧线、事件/知识边界/伏笔版本、风格/声音卡的必要扩展；上下文编译与finding/decision分离。目标：series、library模块与仓储、`application/series_context_*`。依赖：T31–T32；验收 AC-S04–AC-S07。退出：AI建议不自动改正式事实，超长上下文可解释，声音授权合同明确。
- [ ] **T34** 编剧工作台、季集节拍视图、证据跳转和text生成合同；新增功能绑定目录、OpenAPI、前端和后端负向用例同步。目标：`features/series`、`ProjectScript.tsx`、`gateway_models.go`与真实功能目录实际位置。依赖：T33、T23；验收 AC-S01、AC-S03–AC-S05、AC-O01。退出：单集候选—采纳闭环通过，未使用整季全文请求。

## P4：导演、可恢复制作与返修

- [ ] **T41** 逐项导演意图/能力支持矩阵、静帧动态分镜、候选与前后镜头对照；扩展现有制作工作台。目标：projects/media、`ProductionBoard.tsx`、`ProjectStoryboard.tsx`、现有FFmpeg路径。依赖：T23、T34；验收 AC-S07–AC-S08。退出：明确预演身份、声音来源和能力边界，不承诺自动锁脸。
- [ ] **T42** 接入revisions纯规则的持久修订/分支/依赖，增加影响分类与明确选择的重做。目标：`modules/revisions`、series仓储与应用、项目历史/媒体保存协调。依赖：T32–T33；验收 AC-S06、AC-O03、AC-O06。退出：跨集资产改版不覆盖旧采用/封版，事务和CAS合同通过。
- [ ] **T43** 实现productionruns有界DAG、原子attempt/queue/outbox、人工采纳gate、租约与非财务dispatch衔接；暂停/取消/unknown恢复。目标：`modules/productionruns`、`persistence/productionrunrepo`、`application/production_run_*`、现有jobqueue/worker与实际HTTP合同。依赖：T42、T34；验收 AC-O01–AC-O05。退出：多进程故障注入证明不盲目重复POST，无报价/预算代码路径。
- [ ] **T44** 制作矩阵、批量范围/节点预览、返修状态、明确应用剪辑、封版清单/逐集导出和社交结果映射。目标：`features/series`、`ProductionBoard.tsx`、`ProjectMedia.tsx`、`ProjectPublish.tsx`、media/publishing应用。依赖：T41–T43；验收 AC-S08、AC-O04–AC-O06。退出：单人和协作都可完成，取消后结果/高优先级返修行为明示。
- [ ] **T45** 三集合成上游演练和当前全回归，逐项验证能力路由、权限撤销、配置变化、审核失败、未知恢复和手机操作。依赖：T44；验收 AC-O01–AC-O06。退出：确定性软件合同通过，真实模型艺术效果仍标未测；需要真实样片时另行取得授权。

## P5：长期生产容量与运维

- [ ] **T51** 在P0A已实体化/去额度基础上完成长期分区/冷归档、可查询紧凑回执和素材生命周期/GC，清除只读兼容期遗留热点；禁止双真相或按条数强删作品。目标：相关实体仓储与operations。依赖：T45、T07；验收AC-O07、AC-Q01、AC-R04、AC-SCALE。退出：不重新引入额度，正式/unknown引用不清理，恢复和查询不依赖全站历史装入内存。
- [ ] **T52** 公平资源调度、供应商真实并发约束/退避、指标/告警、备份恢复/版本兼容与运行权限演练。目标：jobqueue、worker、operations/backup/migration、部署说明。依赖：T51；验收 AC-O02–AC-O05、AC-O08、AC-SCALE。退出：扩worker不突破资源边界，一个租户不饿死其他租户，无生产迁移操作。
- [ ] **T53** 复现完整200集容量样本与两空间压力、FFmpeg抽样、崩溃/未知与存储恢复；发布可复核报告。依赖：T52；验收 AC-SCALE及所有回归。退出：指标达标或明确阻塞；未通过前不把“可建200集记录”宣传为200集生产就绪。

## 实施时的交付纪律

每阶段记录代码SHA、迁移版本、已通过/失败/未执行的检查与样片边界。涉及供应商请求的测试使用本地合成上游；不创建真实凭据，不收费，不部署，不执行生产数据库迁移，除非后续用户另行授权。后续推送和镜像发布按当时授权单独处理。

2026-11-05用户已明确授权开始实施并要求保持C端视觉一致。本批完成范围及剩余限制见[实施记录](../specs/2026-11-04-creative-series/implementation.md)；未达到完整退出条件的任务保留未勾选，不能把首批交付当成整个P0A或长期项目完成。没有预估强行承诺的日程，实际工期需在对应阶段的接口/迁移与样片范围确定后评估。

## 第二阶段记录

P1实际实现与验证边界见[提示词实施记录](../specs/2026-11-04-creative-series/prompts-implementation.md)。T14相关回归通过，完整网关浏览器18项、HTTP合同447+76、前端51项单测及PG/文件/race验证已记录；后续P2/P3不能仅凭此标记完成。

## 第三阶段记录

P2变量及参考图配方已交付，见[参考配方实施记录](../specs/2026-11-04-creative-series/references-implementation.md)。全量后端race含本地PG通过，网关桌面/手机浏览器22项、前端51项单测、439+87个HTTP合同响应和OpenAPI校验通过。应用与来源固定、参考版本及顺序、真实multipart参数和无价格配置路径已验证；P0A全局scope/分段渲染等及所有P3–P5仍未完成，不能提前推送。

## 第四阶段记录

T03的文件资产库已改为按资产追加的原子事务记录，保留不可变历史与原回执，兼容旧格式升级和文件/PG离线迁移，实际超过64MiB回归通过。证据与限制见[资产库存储记录](../specs/2026-11-04-creative-series/storage-implementation.md)。其他领域scope、上传会话、S3 multipart及渲染检查点仍未交付，T03–T07保持未勾选。

## 第五阶段记录

T05可恢复上传已交付：独立会话/分块实体与文件/PG适配、跨刷新/实例恢复、永久媒体回执、临时生命周期、前端两处上传入口及完整SHA校验。证据与升级合同见[上传实施记录](../specs/2026-11-04-creative-series/uploads-implementation.md)。实际184,320,044字节/120秒PCM全链路及解码通过；S3原生multipart与渲染持久检查点仍未交付，不将T05/T06整体勾选完成。

## 第六阶段记录

T05的S3原生multipart子项已实现：持久upload ID、分段摘要核对、丢失响应恢复、条件完成和临时分段清理，文件/PG与备份迁移已注册。详见[S3实施记录](../specs/2026-11-04-creative-series/s3-implementation.md)。T05整体尚未完成，T06持久渲染检查点及P3–P5保持未勾选；不提前推送。

## 第七阶段记录

T06核心已实现：有理数全局时码、core/转场分段、PCM单次AAC、独立检查点、文件重启与PG接管、稳定产物身份、显式继续和原时间轴约束，保留C端原有风格。详见[渲染实施记录](../specs/2026-11-04-creative-series/render-implementation.md)。全量race、桌面/手机16项及1080p60秒/横竖4K120秒完整编码解码通过，T06按本地渲染范围勾选；T03–T05/T07及所有P3–P5未整体完成，不推送。T06使用已有流式链路和输出档提前推进，不表示其余依赖已全部完成。

## 第八阶段记录

T31 的领域、独立文件/PG目录、CAS排序、原回执、每集Project原子保存与存储切换迁移已实现；201条目录、并发、SIGKILL交接恢复和应用离线往返通过，详见[系列存储基础记录](../specs/2026-11-04-creative-series/series-storage-implementation.md)。HTTP与C端目录、外部集号/实际输出快照未接入，因此T31保持未勾选；T32旧多集拆分、连续性、导演工作流、DAG和200集完整容量未完成，不提前推送。下一阶段先完成T31的可用前台与合同，再按依赖推进T32。

## 第九阶段记录

T31元数据工作区闭环已完成：系列/季/集HTTP与C端页面、当前项目标题、原请求恢复、过期游标拒绝、单集本地编号、输出默认值、真实text单集意图、PG队列传递和不可变导出编号；支持交换及插入顺移，两季200集与新增201集元数据验证通过。证据见[系列工作区记录](../specs/2026-11-04-creative-series/series-ui-implementation.md)。因此T31按其元数据退出条件勾选，不表示T07或AC-S01的后续正式事实串联已经完成，也不表示200集实际制作容量达标。下一阶段T32仍未勾选；旧多集拆分尚未实施，不能推送整套方案。
