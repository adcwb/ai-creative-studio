# 项目路由加载与草稿取消竞态修复

基线：`e40e9eaf4f1b54134419ed62664655679d679d32`。本次仅本地修复，未推送、部署、迁移真实数据或调用真实模型。

## 根因与复现

对应 [CI run 40826832328](https://github.com/adcwb/ai-creative-studio/actions/runs/40826832328) 的 PostgreSQL 手机浏览器在重新打开项目的剧本步骤后找不到 `.drama-script`。该 run 没有上传 trace 制品；本地保留了真实 PostgreSQL、独立 API／worker 的复现 trace。

单次原始10项流程通过，但各运行5次 worker 流程时，桌面5/5、手机4/5，同一第33行失败。失败 trace 显示 URL 已是 `/script`，详情 GET 返回200、版本3、完整3场剧本与3个分镜；界面却显示已生成与空剧本，并误判存在未保存简报。因此不是 worker 未完成、服务端数据丢失或网络等待不足，`/dev/null` 密码文件警告和 npm 提示也不是此故障根因。

`ProjectEditor` 在步骤变化后调用取消简报／剧本／分镜编辑。React 路由切换的 effect 可以保留加载中 render 的回调；项目请求随后已接受新 baseline，但旧回调仍从闭包读到 null，覆盖刚接收的本地标题、剧本和镜头。持续等待不会修复这个状态。

## 修复

`useDramaProject` 在接受新 baseline 的同一处同步更新 ref，三个取消操作在执行时读取这个最新已接受值。已有旧版本拒绝、脏草稿保护、权限、幂等及服务端行为保持原逻辑；没有改动原 worker 测试、超时、重试、skip 或断言。

新增仅供浏览器测试使用的 hook harness：保留加载前的三个取消回调，先确认 HTTP 项目加载完整，再调用旧回调，断言标题、剧本、镜头仍完整且 dirty=false。旧实现桌面／手机均确定失败；修复后通过。夹具由 Vite 正常提供，不修改浏览器网络安全设置，不进入生产构建入口。

## 最终验证

- 新确定性回归：旧实现2项失败，修复后2项通过，包含在完整26项路由测试中。
- 完整路由：26项通过，含桌面／手机草稿导航阻止、放弃编辑、浏览器历史和工作空间切换。
- 原始真实PG worker流程：修复后桌面5/5、手机5/5通过；独立重复验证不作为CI自动失败重试。
- 完整真实PG认证／worker：10项通过，45.1秒；fresh synthetic database、独立worker、shared-file素材，零真实模型调用。
- 前端 typecheck、lint、40项单测及生产 build 通过。
- 后端相关 `go test -race -count=1 ./internal/application -run 'Test(Project|Storyboard|PostgresQueuedCreationAndWorkers)'` 通过，2.550秒，启用真实PG测试DSN；server构建通过。后端源码无修改，未将此专项称为全部后端回归。
- 项目／分镜／创作26项端到端全部通过（2.3分钟），覆盖保存响应丢失、版本冲突、未保存草稿、明确放弃与刷新恢复。

原始日志、基线失败 trace 与修复后重复 trace 保存在云工作区 `/workspace/worker-route-fix-evidence/`。这些 trace 包含仅测试用的合成会话数据，不纳入源码提交。工具链恢复至 `/tmp/studio-fix-tools/`，仅使用隔离合成PG数据库；没有改动用户真实数据。
