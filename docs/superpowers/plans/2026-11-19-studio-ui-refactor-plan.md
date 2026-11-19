# 实施计划 · 映创工作台 UI 重构与长内容治理

规格：`docs/superpowers/specs/2026-11-19-studio-ui-refactor/`
基线：`fce862b9b98fc3cd8ab6d1cecb00d74597599052`（分支 `refactor/workflow-structure`）
状态：**Implementing** — S1 已完成并验证，S2 起未开始

## 步骤

- [x] **S1 主题单源**：单一色板、暗房/工作室双主题、旧偏好迁移（2026-11-19 完成）
- [ ] **S2 壳层骨架**：三栏插槽、吸顶操作栏、底部推进条、共享壳断点收敛
- [ ] **S3 原子封装层**：11 个 Studio\* 组件、图标补齐、清除 `.ant-*` 覆盖与 `!important`
- [ ] **S4 门面三页**：项目列表、创作工作台首页、登录
- [ ] **S5 长内容底座**：虚拟滚动选型实测、大纲、滚动联动、页内搜索、URL 位置
- [ ] **S6 剧本页样板**：分片编辑、memo 化、双栏对白、登场人物横条
- [ ] **S7 生成状态层**：阶段语义修正、错误分层、三张状态表、参数快照
- [ ] **S8 复用 A**：分镜、镜头制作、素材库
- [ ] **S9 复用 B**：任务与历史、审片、提示词库、模型管理
- [ ] **S10 收口**：文档与门禁全量同步

S4 与 S5 都只依赖 S3，可并行起步。S8 需要 S6 与 S7 都完成。

---

## S1 变更说明

### 新增

`frontend/src/ui/palettes.ts` 是全站唯一色值来源，导出暗房与工作室两套色板、`themeVariables()`、`mobileVariables()` 与 `applyTheme()`。camelCase 键一一映射到既有 CSS 变量名，没有手工对照表，因此不存在漏项或拼写漂移。`applyTheme()` 返回还原函数，让嵌套的启动壳与账号壳在卸载时正确交还控制权。

`frontend/src/ui/palettes.test.ts` 把对比度门槛写进单测：两套主题各 18 组文字配对（≥4.5:1）与 18 组图形配对（≥3:1），另断言色板键完整、变量名映射正确、暗房悬停必须加深。今后改色若破线，单测直接失败。

### 修改

`StudioTheme.tsx` 改为从色板派生 antd token 与组件级 token，并在 `useLayoutEffect` 中把同一份色板注入文档根元素。新增 `colorBgLayout`、`colorBgSpotlight`、`colorTextLightSolid`、`colorLink` 与 Modal / Card / Collapse / Alert 的组件 token；Tabs 的深色硬编码色值改为 `link` 与 `rail` 令牌。

`preferences.ts` 的 `Theme` 收敛为 `dark | light`，新增 `resolveTheme()` 承担旧值迁移，`defaultTheme` 为 `dark`。另加设备级绘制提示键 `studio.appearance`，只存主题名，用于会话解析前的首屏着色；账号键仍是唯一权威，不跨账号读取。

`tokens.css` 移除全部颜色定义，只留字体、字号、间距、圆角。`layouts/themes.css` 删除 paper / screening 两个色块与 `.sample-*` 预览色块，主题预览改为由各自色板经内联 CSS 变量着色，选项网格从三列改两列。

`PreferencesPage.tsx` 的主题名改为「暗房 / 工作室」，选项按钮加 `data-appearance` 供测试定位（替代已删除的 `.sample-*` 类）。`locales/appearance.ts` 同步英文值。`index.html` 的 `theme-color` 改为暗房画布，并由 `applyTheme()` 随主题更新。

顺带清掉 7 处绕过令牌的兜底色值与 3 个未定义变量引用：`--primary-contrast` → `--on-primary`，`--line` → `--border`，`--surface-subtle` → `--surface-soft`，以及 PosterEditor / timelinePreview / library 三处 `var(--border, #…)` 的硬编码兜底。现在全部 68 个被引用的 CSS 变量都有定义来源，只剩 `--radius`（几何令牌，留给 S3）。

### 修复的可访问性缺陷

工作室的焦点色 `#8a651f` 对深绿侧栏只有 **2.32:1**，低于 `e2e-routing/design-system.spec.ts` 自己断言的 3:1 门槛；`docs/brand.md` 记录的 `#ad853b` 才对应 3.63:1，说明令牌与文档此前已经不一致。改为 `#a97f2a` 后对白底 3.65:1、对画布 3.39:1、对侧栏 3.37:1，三处同时达标。本轮无法确认该门禁此前是否为红，因为审计阶段没有运行浏览器。

### 测试同步（改断言目标，未删断言、未降门槛）

审计时只找到 3 处硬编码三主题，实际有 **5 处**，补全后全部改为双主题：

| 文件 | 改法 |
| --- | --- |
| e2e-auth/appearance.spec.ts | 主题数组、循环上界、6 处期望值 |
| e2e-consumer/consumer-polish.spec.ts | 主题循环数组 |
| e2e-gateway/workflows.spec.ts | 主题循环数组 |
| e2e-routing/design-system.spec.ts | 主题循环数组 |
| e2e-routing/shell.spec.ts | 数组、选择器、2 处期望值 |

`preferences.test.ts` 重写为 4 个用例，覆盖账号隔离、旧值迁移、存储被拒、绘制提示与账号键分离。

### 文档同步

`docs/brand.md` 更新定位句、色源说明、双主题色表、非颜色令牌表与实测对比度，并记录焦点色修复。`docs/design/2026-10-31-appearance.md` 加「2026-11-19 部分替代」注记，明确替代范围只限主题数量、色值与色源，其余身份隔离与存储约定继续生效；历史记录本身不改写。

---

## S1 验证证据

实际执行的命令与结果：

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过，无错误 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 29 文件 / 97 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3431 条文案 |
| `npx vite build` | 成功，2155 模块 |
| routing design-system · desktop · dark | 通过（44.9s） |
| routing design-system · desktop · light | 通过（46.7s） |
| routing shell · desktop · zh-CN/dark | 通过（33.4s） |

两个 design-system 用例是仓库既有的对比度与几何门禁，覆盖 tab 文字 4.5:1、输入框边界 3:1、输入文字 4.5:1、说明文字 4.5:1、主按钮默认与悬停 4.5:1、侧栏控件边界 3:1、**侧栏焦点环 3:1**、导航触控目标 ≥44px、无横向溢出。两套主题均通过。

主包体积 638.19 kB（gzip 213.05 kB），比审计记录的 634.62 kB 增加约 3.6 kB，来自色板与注入逻辑。chunk 体积警告是既有状态，按既有性能规格继续保留，未调高阈值掩盖。

截图证据：暗房与工作室的模型管理页、暗房的偏好设置页（含两个主题预览卡）。

### 本轮未运行

mobile 设备项目、routing 其余 spec、e2e / e2e-auth / e2e-consumer / e2e-gateway 四套（需真实 Go 后端）、Go vet 与三组 race 测试、file × postgres 双存储矩阵。**本机通过不等于完整 CI 矩阵通过。** 后端本轮零改动。

### 环境说明

会话 Linux 环境无 Go 工具链，`backend/bin/server.exe` 是 Windows 二进制，因此无法运行需要真实后端的四套 E2E；routing 套件用 Node 假上游，可以运行。为在此环境跑通 vitest / vite / Playwright，向已被 gitignore 的 `node_modules` 补装了 Linux 版 rollup 与 esbuild 原生包、Chromium 及 `libXdamage1`（解压到临时目录，未改系统）。`package.json` 与 `package-lock.json` 未改动。构建产物输出到临时目录，仓库 `frontend/dist/` 未被改写。

---

## S1 已知遗留

34 处 `.ant-*` 覆盖与 6 处 `!important` 仍在 `ui/studio-ui.css` 等文件中，按计划在 S3 由组件 token 消化。`--radius` 未定义（Avatar3DStudio 兜底 12px）留给 S3 的几何令牌清理。5 处原生 `<dialog>` 尚未收敛到 `StudioDialog`，同样在 S3。

`auth.css` 的 51 处硬编码色值未动，登录页整体令牌化排在 S4。