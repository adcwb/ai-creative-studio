# 贡献指南

- 从已同步的主分支创建简短主题分支；提交和推送须遵循仓库所有者授权。
- Go 使用 `gofmt` 和 `go vet`，按职责放在 internal 下，错误不得静默吞掉；TypeScript 启用严格类型，遵守 ESLint Hooks 规则。
- 保持 PR 聚焦，描述问题、行为变化、验证证据和剩余风险。建议 Conventional Commits（如 `feat: add image task workflow`），但不强制自动发布。
- API 变化同步 `api/openapi.json`、前后端调用、测试和文档；不要宣称 mock 验证了真实模型能力。
- 不提交 `.env`、凭据、生成素材、用户数据、构建产物或测试数据目录。不要添加未授权许可证。
- 提交前运行 README 中检查；CI 不能替代真实授权、安全设计或生产验证。
- 漏洞或敏感信息请使用 GitHub 的私密安全报告渠道（若仓库已启用）；不要在公开 Issue 中放凭据或客户数据。
