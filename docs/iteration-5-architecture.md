# 第五轮架构：已保存剧本到分镜计划

基线为已发布的 `b080d6bfe9e48f1066b2beb37f530fa5eaa28247`。接口权威来源是 [OpenAPI 0.5.0](../api/openapi.json)。本轮只实现与本地验证，不推送、部署、配置凭据或调用付费模型。

## 模型与版本

沿用第四轮 Project、结构化 Script、项目域 receipt、FileStore 和单写者锁。每项目一份当前剧本及一份当前分镜，不建立重复项目模型、历史计划版本库或视频时间轴。

Project 增加 `script_version`、`storyboard_stale`，可选 `storyboard` 和 `latest_storyboard_generation`。没有剧本时 `script_version=0`；首次剧本生成成功为 1，以后仅剧本保存或重新生成成功递增。项目需求编辑和分镜编辑不改变剧本版本。已有 v2 剧本迁移时从 1 开始，不伪造历史修订次数。

`Project.version` 仍是所有内容写入的乐观锁；项目、剧本、分镜保存以及两种生成成功均递增。接受、运行、失败不递增。两种版本用途不同：`version` 阻止并发覆盖，`script_version` 判断分镜来源是否过期。

当前分镜包含 `source_script_version` 与 `shots`。来源由服务端设置，保存请求不得提交或覆盖它。服务端比较来源与当前剧本版本生成 `storyboard_stale`；无计划时为 false。过期计划可继续编辑保存，但保留来源和过期提示，只有基于当前剧本重新生成成功才更新来源。

原有剧本场景没有 ID，故镜头使用 `scene_index`（从 1 起）引用来源剧本的场景位置。此引用必须与 `source_script_version` 一起理解；不是跨剧本版本稳定的实体 ID，也不能在计划过期时把当前场景文字冒充原来源。

| 镜头字段 | 约束 |
| --- | --- |
| `id` | 1–40 个 ASCII 字母、数字、下划线或连字符，计划内唯一 |
| `scene_index` | 整数 1–3，对应来源剧本三场 |
| `description` | trim 后 1–1000 Unicode 字符 |
| `shot_size` | `wide / medium / close_up` |
| `camera_movement` | `static / pan / track` |
| `duration_seconds` | 整数 1–60 秒 |
| `dialogue` | trim 后 0–2000 Unicode 字符，可空 |

每计划 1–30 镜；接口继续受项目 64 KiB 请求体限制，因此字段上限不是承诺所有字段同时达到上限。拒绝未知字段、null、类型错误及非法引用/重复镜头 ID。对白是镜头文本，不增加角色资产或语音模型。

## HTTP 与执行

| 方法与 `/api/v1` 下路径 | 输入与返回 |
| --- | --- |
| GET `/projects/{id}` | 当前项目，包含计划、过期标记与最新尝试，供轮询 |
| GET `/projects/{id}/storyboard-generations` | 生成尝试数组，无尝试返回 `[]` |
| POST `/projects/{id}/storyboard-generations` | `{expected_version}`；新接受 202，幂等重放 200 原尝试当前状态 |
| PUT `/projects/{id}/storyboard` | `{expected_version,shots}`；200 当前 Project |

所有路由遵循既有 `space_id` 归属校验，跨空间/不存在返回 404。所有写操作要求 `Idempotency-Key`，与项目创建、需求编辑、剧本操作共用同一项目域键空间。精确回执重放先于版本和活动生成检查；保存重放返回当前投影，不重写旧内容。网络结果不明时只能重试相同 body/key，不能更新版本或自动换键覆盖。

409 沿用 `version_conflict`、`generation_active`、`idempotency_conflict`、`script_missing`，新增 `storyboard_missing`。前端保留草稿，只有显式放弃并重载才替换。其余错误保持原项目协议。

生成读取已保存剧本，在入队时持久化不可变 `source_script` 快照、`source_script_version` 和 `base_version`。公开尝试包含 ID、归属、版本、固定 `provider=mock-storyboard`、阶段与安全错误，不公开内部剧本快照。Eino 流程校验输入、确定性模拟生成、校验输出后原子提交当前计划与成功状态。页面明确标记模拟，不能将程序产物表述为真实模型或视频。

两种生成中的任一种 queued/running 都阻止该项目所有新的内容写入和另一种生成；已接受请求仍能重放。完成时还必须在同一锁内复核项目版本、源剧本版本和最新尝试关联，避免迟到结果覆盖新内容。失败仅保存尝试错误，保留旧计划、来源和内容版本。保存的剧本场景动作含 `[mock-fail]` 可触发 `mock_storyboard_failed`；不自动移除标记。非法输出使用 `invalid_storyboard_output`。

## 持久化与恢复

快照升级 v3，新增必需的 `storyboard_generations` 映射。完整 v1/v2 经校验后迁移，保留图像、项目、剧本及回执；缺失/null 必需映射和不完整快照不能被默认空库掩盖。既有剧本补 `script_version=1`，无剧本为 0。回退必须停服并恢复匹配版本的整个 DATA_DIR 备份，不能改版本号降级。

生成排队、领取、完成与写回执沿用独立快照、深复制、临时文件/fsync/rename 以及持久化成功后更新内存的机制。分镜 mock running 在重启后以同一次尝试重新排队；成功提交只增加一次版本。原图像真实调用中断不自动重发的保护保持不变。

没有引入数据库、多实例、生产认证、企业隔离、容量治理或整机断电事务保证。计划和剧本仅保存当前稿；生成尝试历史不是可回滚版本库。

## 验证边界

`scripts/contract_check.py` 使用随机回环端口、临时二进制和数据目录验证真实响应与 OpenAPI，覆盖生成、保存、字段边界、跨空间、版本/幂等冲突、源版本过期、失败保留、重启后回执及重新生成消除过期；保留旧图像/剧本合同检查。它不启用任何可调用外部模型的有效配置。最终执行结果见第五轮验证记录，契约数量中的轮询响应不能当作独立用例数。
