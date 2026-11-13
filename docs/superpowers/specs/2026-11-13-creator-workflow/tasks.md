# 任务与可测试验收

- [x] T1 / AC1：Given 当前剪辑和历史修订，When 预览并恢复某镜头、声音、字幕或完整剪辑，Then 只改选定部分；CAS/幂等/跨租户/撤权/损坏素材/非法时码均正确，旧成片不改。目标 revision/media 应用、RevisionWorkspace；前置无。
- [x] T2 / AC2：Given 项目固定资产版本及更新后的库，When 读取参考版本核对，Then 显示需要复查的绑定和固定/当前版本，不自动换图、不恢复撤销授权。目标 library binding 与 ProjectReferences；前置无。
- [x] T3 / AC3：Given 一季多集、部分完成及有未解决审片，When 打开制作概览，Then 当前页状态真实、可进入各工作区，缺失/错误明确，零模型调用。目标 series HTTP/UI；依赖既有目录、T2。
- [x] T4 / AC4：Given 已成功正式导出及字幕，When 下载交付 ZIP，Then 视频/SRT/VTT/manifest 字节 SHA 正确、时间轴固定、无路径泄露；预演/失败/跨租户拒绝，旧导出及当前剪辑不变。目标 media HTTP、MediaStudio；前置无。
- [x] T5 / AC5：Given 个人/团队及四角色，When 查看能力、团队改名/退出，Then 权限/CAS/CSRF正确；个人不共享、所有者不能退出；旧数据/跨实例撤权语义保持。目标 auth HTTP、SpacesPanel；前置无。
- [x] T6：受影响 Go/PG、前端单元/类型/lint/构建、API 合同、桌面/手机交互和截图检查；更新本文记录，审查完整改动并安全推送、跟踪 CI。依赖 T1–T5；不能以模拟供应商冒充真实效果。

后续阶段（本次五项通过并推送后继续；先不推送）：
- [ ] N1：Dify 式画布，单项目可视化编辑、版本/图验证、真实节点和人工确认、可恢复执行；不能将 Eino 后端链冒充现有画布。
- [ ] N2：数字人接入及产品闭环，明确实际执行器/供应商合同与许可，调整可支持的旧边界，真实授权/效果未取得时标未验收；不生成假数字人。

T1–T5 已完成本轮闭环；具体保留及未验收边界见 implementation.md。T6 已完成，本地验证及远端 25 项 CI 均已通过；本地全量 routing 首轮结果与 8 项定向复验分开记录于 validation.md。
