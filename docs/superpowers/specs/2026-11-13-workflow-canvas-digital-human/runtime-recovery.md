# 运行输入、局部重做与数字人恢复增量

状态：2026-11-14，本地实现与验证；不推送、不部署。代码起点 `3c3ea81cf9860b4ec51d415e851c6ce44b4ea1da`，包含已发布master `99697987c75ce7a073adbad879c8e1bd425ab832` 的准确祖先关系。本文件补充原N1/N2规格，不把完整路线标记为完成。

## 创作者流程与验收

- RC1 MUST：创作者为流程声明变量名称、展示名称、必填和默认值；每次运行填写本次输入。保存模板不调用模型，运行固定解析后的输入和摘要，旧运行不变。未知字段、重复/保留名称、NUL、非法UTF8拒绝。模板只按字面替换一次，不执行表达式、不重新解释值内大括号；保留既有CreativeJob 2000字符工作窗口，不宣称支持供应商全部token窗口。
- RC2 MUST：文本模型节点选择当前空间真实网关文本能力，通过新增 `workflow_text` 功能绑定与既有CreativeJob/队列执行，处理要求与上一步文本分别发送，JSON文本结果进入人工确认再输出。无绑定、模型禁用、能力不匹配、撤权均拒绝，绝不退回mock。模型选择器和运行表单在桌面及手机均可操作。
- RC3 MUST：导演在已结束运行选择“从此步骤重新制作”，显式确认将重做的下游；服务端新建运行，原结果保留。仅成功且不在下游的节点复用，选中节点及其下游重新排队；任何未知结果、其他未完成分支、过期项目版本均拒绝。必须保持同一修订和输入，修改设计应另存修订。新模型调用使用当前授权且准确版本，恢复同一原请求不创建第二个新运行。
- RD1 MUST：用户为项目已有音频单独声明声音授权依据并确认，授权绑定项目/空间/音频与声明人，可永久撤销。新数字人任务必须指定该授权，实际进程派发和结果发布前重新核对当前授权/成员权限。撤销不冒充已撤回上游计算、不删除历史作品，也不验证法律文件真实性。
- RD2 MUST：数字人提交、撤销和恢复的JSON请求在当前用户/空间/项目的sessionStorage保留同键同body；刷新后仅核对原请求，未知结果不能静默重发。原始File上传本身不跨刷新恢复，不把本功能称为分块上传。
- RD3 MUST：本地files-v1在派发前持久记录dispatching；只在确认实际引擎输出并检查媒体后保存engine_done，确认合成后保存composed。保存私有文件/受管blob的大小、哈希、作用域和来源摘要，文件和PG均可跨进程恢复后期处理，恢复不得再次调用引擎。无确认输出的dispatching、损坏证明、来源变化、授权撤销均拒绝续跑，保留unknown核对语义。

## 领域/API与实现位置

`modules/workflows/inputs.go` 的Variable、Graph.variables、Node.variable_name与Run.inputs/inputs_sha256保持运行快照；`modules/prompts`纯编译器复用词法，无I/O。`workflow_text.go`接入原runtime，`workflow_rerun.go`计算下游和复用，workflowrepo校验固定来源链。HTTP `POST projects/{id}/workflows/{workflow_id}/runs`增加inputs；`POST projects/{id}/workflow-runs/{run_id}/reruns`接受node_id、expected_version、confirmed，必须原请求键。

`digital_human_voice.go`提供项目voice-authorizations列表、创建和/{voice_id}/revoke，当前RBAC/CSRF/租户中间件继续生效。新任务类型 `digital_human_authorized` 要求 voice_authorization_id、确认者与依据；保留旧 `digital_human` 每任务声明兼容，旧任务不自动拥有可全局撤销的独立授权。

`digital_human_checkpoint.go`实现私有检查点、流式证明和恢复；API仅输出avatar_recovery_phase，不泄露路径/blob键。`publicCreativeJob`只用于HTTP序列化，内部getter保留运行时选择等私有字段供原链路使用。`blob_migration.go`与blob_manifest包含中间产物，避免迁移后丢失恢复证明。文件写入采用不可覆盖私有文件；PG使用既有租约检查。VoiceAuthorizationPanel、avatarPending与WorkflowInputs/WorkflowRerun复用现有组件和原请求恢复交互，没有新建主题或更换导航。

## 升级、迁移与故障语义

新增 `studio_voice_authorizations` 表及project复合外键、就绪检查。必须先停止所有API/worker writer，备份数据库和DATA_DIR，再按既有离线store-migrate/init流程升级，所有实例一起启动匹配二进制。旧工作流无variables仍保持原摘要；不认识新节点/任务的旧worker不可参与混合运行。新增独立任务kind避免旧worker把新授权字段忽略后当旧任务执行；这不等于支持滚动混合版本。

PG↔文件状态迁移保留授权与撤销；私有检查点二进制迁移须沿blob迁移清单完整复制并校验，单独复制JSON不够。回滚须同时恢复匹配二进制、数据库和DATA_DIR。未执行任何生产迁移。

在进程返回与检查点提交之间崩溃仍可能只留下dispatching，必须人工核对；不承诺恰好一次外部计算。取消/撤销不会保证正在执行的本地引擎立即停止；发布与新派发会被阻断。中间结果为私有引用，暂不新增自动GC策略，部署需沿现有运维流程管理空间。恢复保留来源链，破损原始证明不使用另一份产物猜测成功。

## 明确未完成与外部验收

专用云数字人供应商、异步云任务恢复、原生窗口分段、完整专用take审片台、实时音视频数字人、3D资产/动作/渲染合同与执行器仍未实现。当前是本地files-v1已确认输出的恢复，不能冒充这些能力。没有真实付费模型、GPU/权重、真实声音/肖像商业许可、真实S3、原生Windows运行、原生4K效果或连续五集艺术验收。真实模型和许可缺失不阻止交付已验证本地代码，但也不能将上述代码缺口仅归因于缺凭据。

## 验证

最终命令、结果及截图见validation.md最新增量；测试使用隔离账号、合成本地上游、文件存储和本机真实PostgreSQL，未访问生产数据或付费模型。本分支未推送，无对应新云端CI或镜像结果。
