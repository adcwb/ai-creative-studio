# 工作室导航、登录画廊与网关配置收敛

基线 `8c707240f36eec16fc29be823efeebc7509182e3`，工作树及远端一致。本轮仅本地开发，不推送／部署，不读取真实密钥，不执行真实供应商调用。仓库和 `/workspace/.agents` 未发现适用的 AGENTS.md / SKILL.md。

## 登录

已查看原始 SVG 实际截图 `/workspace/design-20261031/login-before.png`，并逐张查看前轮及本轮图像。恢复旧稿最鲜明的深森林绿、奶油白、倾斜层叠画框与前景小画片；新素材增加纸张层次、近中远景与光门投射，而非仅添加 CSS 阴影。三个主题继续为图像创作、分镜镜头、明确标注规划中的交互数字人。网页渲染双语文案，无烘焙文字。

原代码本来以 `paused=false` 启动，但画廊 focus capture 和页码选择会隐式写入暂停；系统 reduced-motion 也会停播。没有证据证明所有普通首次访问均失败。删除页码及其隐式暂停路径；每次挂载自动启动，不恢复暂停。保留一个低干扰暂停按钮，系统减弱动画时保持静态。不以强制动画覆盖系统无障碍偏好。测试分别观察真实八秒定时器、刷新后自动播放和 reduced-motion。

原生 1536×1024 PNG 已保存至仓库 `docs/design/sources/login-2026-10-31/{vision,sequence,presence}.png`（WebP 仅压缩，不放大）。生成任务原文件：
- 图像：`/workspace/generated_images/exec-a2f91e72-f985-4f95-bd25-2a896ac53b5d.png`
- 分镜：`/workspace/generated_images/exec-0c8713a4-ef61-4a8f-a3cd-fa70a2cb01b9.png`
- 数字人：`/workspace/generated_images/exec-3bdb4c8c-932f-45c4-b2bb-fbb21e66af8d.png`

## 导航与配置边界

登录后语言入口统一到系统设置。桌面侧栏可缩为图标栏；菜单组独立折叠，原生 button + aria-expanded，当前组高亮。手机保持抽屉、Escape 和焦点循环；隐藏组内元素不再进入焦点循环。折叠状态按账户写入当前浏览器，退出／切换账户重新挂载，不宣称跨设备同步。共享页眉显示具体路由／项目步骤及组织与角色上下文。

系统设置新增组织运行配置区域，复用服务端网关 API 与权限／版本机制，不创建第二套存储；仅 owner/admin 可维护。独立模型管理路由保留。新增网关直接展示 API Key 输入；编辑默认保留已有 Key，不读回明文。缺少部署 KEK 时明确说明变量名与重启要求，禁止提交加密凭据；可以显式选择 keep 仅保存连接元数据。保存时清空输入，未知保存结果要求先重新读取版本，不自动重交。连接检查由显式按钮触发，仅 GET 模型目录，不代表生成质量或付费授权。

## 旧配置迁移

移除旧供应商表单及组件。旧 PUT `/model-config` 返回 410；GET 和本地验证继续用于历史记录，存量密文、版本和既有执行适配器未删除。新增管理员 POST `/model-config/revoke` 仅按 ID / expected_version 显式撤销历史凭据，不可新增或改写旧模型；仍需会话、组织权限、Origin、CSRF 和审计。新任务配置通过网关与模型维护。

不会把 Anthropic Messages、即梦签名或其他厂商专用协议静默改写为 OpenAI-compatible。历史配置仍可显式选择并遵循治理与权限。需要新接入这些专用协议的用户应使用实现相应兼容能力的网关；不能仅改模型 ID 宣称原生协议支持。回滚恢复匹配版本和备份，不需数据库 schema 迁移。

## 真实浏览器发现的 Key 更新根因

跨端口真实 API 测试复现：POST 创建成功，但 PATCH 修改在 CORS 预检被浏览器拒绝，控制台明确报告 `Method PATCH is not allowed by Access-Control-Allow-Methods`。两层 Handler 的方法列表遗漏 PATCH；本轮补齐并让 CORS 回归发送 PATCH 预检。角色、CSRF、审计、版本检查不变。

注意当前仓库既有策略是回显所有非空 Origin 并允许凭据，FRONTEND_ORIGIN 仅约束 SSO 回跳（见 authentication.md），并非来源白名单。本轮未扩大或重写该既有策略；不能把此次修正描述为已实现严格来源白名单。生产部署需单独评估该边界，尤其同站点不可信子域。
