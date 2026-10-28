# 映创前端

## 启动与验证

```sh
npm ci
npm run dev
npm run typecheck
npm run lint
npm test
npm run build
npm run test:routing
```

`VITE_API_BASE_URL` 指定后端地址。开发服务默认使用 `127.0.0.1:5173`。

`test:routing` 自动启动 Vite 和仅监听本地地址的合成 HTTP 服务，验证真实浏览器中的路由与交互。首次运行需安装 Playwright Chromium；也可设置 `PLAYWRIGHT_BROWSER_CHANNEL=msedge` 使用本机 Edge。该套件覆盖桌面、手机、1440/1920 宽屏、刷新与浏览器历史、权限展示、组织切换、项目和管理表单的草稿保护，以及图像请求幂等键保留。夹具依据现有响应类型和仓库接口契约构造，不验证真实后端持久化、模型调用或社交平台发布。

原有 `test:e2e`、认证、费用与 PostgreSQL 套件仍使用真实后端，需先在受支持的环境构建 `backend/bin/server`；Go 后端目前不能直接在此 Windows 环境编译。

## 页面与代码边界

| 路径 | 职责 |
| --- | --- |
| `/studio` | 图像创作 |
| `/assets` | 已保存的图像素材 |
| `/projects` | 短剧项目列表 |
| `/projects/new/brief` | 创建短剧项目 |
| `/projects/:projectId/:section` | 项目简报、剧本、分镜、素材与成片、发布 |
| `/settings/organization` | 组织与成员 |
| `/settings/models` | 模型配置，仅所有者和管理员可访问 |
| `/settings/billing` | 分币种预算与费用 |
| `/social-callback` | 社交账号授权回调 |

`App.tsx` 声明路由，页面模块按路由加载；`layouts/` 管理应用外壳；`app/` 提供会话、组织上下文与导航保护；`pages/` 组合路由入口；`features/images/` 保存图像工作区状态；`features/projects/` 管理项目编辑状态及各步骤；`components/` 保留复用的业务控件。

图像控制器在当前组织的应用生命周期内保留，切换到项目或设置页面也不丢失草稿和请求幂等键；组织切换会重建控制器。项目步骤按当前 URL 挂载，离开有修改、正在提交或结果未知的页面时先提示。发布工具分别注册进行中操作，避免一个工具结束时清除另一个工具的保护。组织选择保存于当前浏览器标签页，并在加载时以服务端可访问组织列表校验。

新增路由应复用应用上下文和导航保护。布局样式限定在 `.app-shell` 中，业务控件继续使用已有设计变量。生产 Nginx 已配置 SPA 路径回退，直接访问或刷新子路由无需新增后端路由。
