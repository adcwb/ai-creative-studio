# 实施计划 · 映创工作台 UI 重构与长内容治理

规格：`docs/superpowers/specs/2026-11-19-studio-ui-refactor/`
基线：`fce862b9b98fc3cd8ab6d1cecb00d74597599052`（分支 `refactor/workflow-structure`）
状态：**Implementing** — S1、S2 已完成并验证，S3 起未开始

## 步骤

- [x] **S1 主题单源**：单一色板、暗房/工作室双主题、旧偏好迁移（2026-11-19 完成）
- [x] **S2 壳层骨架**：三栏插槽、吸顶操作栏、底部推进条、共享壳断点收敛（2026-11-19 完成）
- [x] **S3 原子封装层**：11 个 Studio\* 组件、图标统一、清除 `.ant-*` 覆盖与 `!important`
  - [x] S3-1 原子层 9 个组件 + `.ant-*` / `.adm-*` 覆盖清零 + `!important` 收敛（2026-11-20 完成）
  - [x] S3-2a 任务详情收敛到共享弹层 + 新增可运行弹层门禁 + `--radius` 令牌（2026-11-20 完成）
  - [x] S3-2b 提示词三处弹层收敛、图标混用清零与门禁（2026-11-20 完成）
- [x] **S4 门面三页**：项目列表、创作工作台首页、登录
  - [x] S4-a 登录页令牌化与图标统一（2026-11-20 完成）
  - [x] S4-b 项目列表：吸顶筛选、ProjectCard、三态与客户端检索（2026-11-20 完成）
  - [x] S4-c 创作工作台首页：≥1400px 三栏，其余保持两栏（2026-11-20 完成）
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

---

## S2 变更说明

### 新增

`src/ui/StudioWorkbench.tsx` 是共享三栏骨架：左大纲、中主视区、右检查器，另有两个可选插槽 `toolbar`（吸顶）与 `nextStep`（底部推进）。列轨由组件按「插槽是否存在 + 是否收起」算出，写成引用令牌的 `--workbench-columns`，因此不需要为组合状态写一堆 CSS 分支。阅读限宽是显式 `measure` 选项，默认不限制，避免把卡片网格也压窄。

侧栏面板可收起为 48px 图标轨，状态按账号存在 `studio.preferences.v1:<user>:panels`。**窄屏不另造抽屉**：≤760px 时两个面板改为按钮唤起 `StudioDialog`，复用既有弹层与焦点管理，全站仍只有一套覆盖层实现。离开窄视口时抽屉自动失效，不留残留状态。

`src/ui/StickyActionBar.tsx` 吸顶在壳层顶栏之下；`src/ui/NextStepDock.tsx` 底部吸附。两者都用 `sticky` 而非 `fixed`，不需要占位元素，也不会盖住别的页面。`src/ui/workbench.css` 只管布局，颜色、间距、圆角、层级全部取自令牌，没有任何 `.ant-*` 选择器。

### 修改

顶栏改为吸顶，高度统一由 `--shell-topbar-height` 提供，并在 760px 断点处把该令牌重定义为 60px——吸顶层的偏移因此自动跟随，不需要第二套魔法数字。原先 `style.css` 写 78px、`app.css` 写 68px、移动态再写 60px 的三处不一致就此消除。

新增层级令牌 `--z-sticky / --z-dock / --z-nav-backdrop / --z-nav` 与几何令牌 `--workbench-outline / --workbench-inspector / --workbench-rail / --reading-measure`，替换侧栏与遮罩里写死的 30 / 29；跳转链接改用 `calc(var(--z-nav) + 1)` 以保证压在吸顶栏之上。色板新增 `scrim`，移动导航遮罩不再写死 `rgb(8 24 17 / 40%)`。

`app.css` 剩余 6 处硬编码色值全部令牌化：导航文字与悬停改 `--on-forest`，选中底色改 `--forest-raised`，两处主色上的白字改 `--on-primary`，分组分隔线改 `--forest-line`。至此 `app.css` 零硬编码色值。

删掉 `style.css` 1240px 块里的 `.sidebar` / `.sidebar-inner` / `main` 三条声明——它们始终被 `app.css` 中更高特异性的 `.app-shell` 选择器覆盖，是死代码，也是「共享壳有两个竞争断点」这一错觉的来源。共享壳的移动边界本来就统一在 760/761，本步核实并保留；1150 / 1700 是宽度收紧，与移动边界不是同一根轴。

`UserPreferences` 增加 `panels` 偏好与读写，沿用既有的按账号分域与存储失败提示。

### 首个真实接入

剧本页的「下一步：生成分镜」从正文末尾移入 `NextStepDock`，并置于版本历史之后成为最后一个元素，使其在整页任意滚动位置都吸附在视口底部。这是底座的第一个真实消费者，也是 S6 之前的最小验证。剧本页的三栏、分片编辑与双栏对白仍留给 S6。

### 测试

新增 `src/ui/StudioWorkbench.test.tsx`（5 个用例）：列轨组合、阅读限宽开关、吸顶栏与推进条的可访问名称、面板收起后只留展开轨且不挂载内容、中英文文案。用例用 `renderToStaticMarkup`，并对 antd 做最小替身——断言的是本组件的布局契约，真实 antd 渲染由浏览器套件覆盖；这同时把该文件的导入耗时从 131s 降到 0.9s。

新增 `e2e-routing/workbench.spec.ts`（桌面 + 手机各 1 个用例）作为 S2 的回归门禁：在 300 / 900（桌面）与 600（手机）滚动位置断言顶栏与推进条同时在视口内，且无横向溢出。

**这套断言抓到一个真实缺陷**：`workbench.css` 原先只被 `StudioWorkbench` 引入，而剧本页直接用的是 `NextStepDock`，推进条因此完全没有样式、`viewport ratio 0`。已让三个组件各自引入所需样式（打包器去重）。

---

## S2 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 30 文件 / 102 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3437 条文案 |
| `npx vite build` | 成功，2156 模块，52.4s |
| routing 断点与溢出（640/700/760/761/900/1200） | 通过 |
| routing 对比度与几何门禁 · desktop · dark | 通过 |
| routing workbench · desktop 吸附 | 通过 |
| routing workbench · mobile 吸附 | 通过 |

主包 638.65 kB（gzip 213.23），比 S1 增加 0.46 kB；CSS 90.47 kB（gzip 16.90）。chunk 体积警告仍是既有状态，未调阈值。

截图证据：桌面滚动 900px 时顶栏与推进条同时在位；手机 390px 宽滚动 600px 时同样成立。

### 本轮未运行

routing 其余 spec、mobile 设备的对比度门禁、四套需真实后端的 E2E、Go vet 与三组 race、file × postgres 矩阵。后端零改动。**本机通过不等于完整 CI 矩阵通过。**

### 一处既有行为说明

截图中主区左缘有一条琥珀色竖线，是 `#page-content:focus-visible` 的 3px 焦点描边（路由切换后焦点落到主区），本步之前就存在，不是新增缺陷；因为主区高于视口，只能看到它的左边缘。

---

## S2 已知遗留

`StudioWorkbench` 目前只有剧本页的推进条在用，三栏骨架本体等 S6 接入剧本页时才获得真实页面验证。`--radius`（Avatar3DStudio 兜底 12px）仍未定义，与 34 处 `.ant-*` 覆盖、6 处 `!important`、5 处原生 `<dialog>` 一并留给 S3。

---

## S3-1 变更说明

### 原子封装层

新增 9 个组件，与既有 `StudioSelect`、`StudioDialog` 合成 11 个原子入口：`StudioButton`（四档变体映射到库类型）、`StudioCard`、`StudioTag`、`StudioField`、`StudioInput` / `StudioTextArea`、`StudioEmpty`（无数据 / 无结果 / 无权限三态）、`StudioSkeleton`（文本 / 卡片 / 媒体三型）、`StudioError`。样式集中在 `ui/atoms.css`，只用令牌。

两处与方案原稿的偏差，理由如下。`StudioCard` 保持业务 `section.panel` 而非 antd Card：60 个调用点依赖 `.panel` 的布局契约，换底会连带重写这些页面的间距，属于 S4 之后的迁移范围。`StudioProgress` 推迟到 S7：它的价值在于修正 `progress` 的阶段语义，提前建一个无人使用的组件只是空壳。

### .ant-* 与 .adm-* 覆盖清零

34 处 `.ant-*` 与 1 处 `.adm-*` 全部消除，分三条路径：

**组件 token**（随视口切换，由 `useMobile()` 驱动 ConfigProvider）：移动端 `controlHeight` 44 取代全局 `.ant-btn{min-height:44px}`；`Table` 的 `cellPaddingBlock/Inline` 移动端置 0、`borderColor` 置透明，取代原先带 `!important` 的单元格压制；`Tabs.horizontalMargin` 取代 nav 下边距覆盖，`horizontalItemGutter` 取代相邻 tab 间距；`Checkbox.controlInteractiveSize` 14 取代直接改 input 宽高（原写法会缩小透明点击层，新写法只缩视觉框，命中区反而更正确）；`Button.controlHeightSM/contentFontSizeSM` 承载小号按钮尺寸。

**语义 DOM API**：antd 6 的 `classNames` 让我们把自己的类名挂到内部节点上——`Modal.container`、`Select.input`、`Tabs.item`，以及 antd-mobile `Popup.maskClassName`。因此覆盖内部节点不再需要写库类名。

**业务选择器**：根节点样式改用组件自带的业务类（`composer-submit`、`prompt-optimize-button`、`runtime-picker-action`、`studio-dialog-close`、`studio-checkbox`、`studio-alert`、`studio-textarea`）；表格移动端堆叠改用 `.studio-gateway-table table / thead / tbody / tbody > tr > td` 这类**元素选择器**；最后一格的分隔线移到自己的 `.gateway-actions` 上，从而完全避开与库选择器的特异性竞争。与库默认值竞争的少数规则统一加 `.user-workspace` 前缀，由特异性而非源码顺序决胜。

顺带删掉 `studio-ui.css` 里 `.user-workspace{--adm-*}` 那一整块——它是 S1 之后残留的**第二份色源**，与 `palettes.ts` 的注入重复。

### !important 收敛

6 处减到 1 处。网关单元格的两处由 Table token 取代；`.step-number` 的 `!important` 本无竞争对象，直接去掉并按方案改为等宽数字（该元素在创作区已被隐藏，视觉无变化）；`.delivery-preparation article` 与 `.prompt-checkbox` 的 `display` 改为提升特异性（后者加 `.prompt-fields` 前缀）。

**保留的唯一例外**是 `prefers-reduced-motion` 重置：`*, *::before, *::after { animation: none !important; ... }`。去掉 `!important` 会让组件与库动画重新胜出，等于牺牲无障碍去满足一条风格约束。已在 CSS 里写明这是刻意的例外。

### 测试

新增 `src/ui/atoms.test.tsx`（7 个用例）：变体到库类型的映射、危险态、徽标语义、空状态三态、字段把 hint/error 接到 `aria-describedby` 并置 `aria-invalid`、面板保留 `.panel` 契约、错误面渲染稳定码与操作、骨架屏比例与输入错误态透传。对 antd 做最小替身，断言的是封装层的映射与无障碍接线。

`Icon.tsx` 导出 `IconName` 类型，供原子层约束图标名。

---

## S3-1 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3437 条文案 |
| `npx vite build` | 成功，2159 模块 |
| consumer 网关对比度 · dark/light × 桌面/手机 | 4 个全通过 |
| routing 对比度与几何门禁 · desktop · dark | 通过 |
| routing 对比度与几何门禁 · desktop · light | 通过 |
| routing 断点与溢出扫描 | 通过 |
| routing workbench 吸附 · desktop | 通过 |

静态计数：CSS 中 `.ant-*` 选择器 0、`.adm-*` 选择器 0、`!important` 1（无障碍重置）。

CSS 体积从 90.47 kB 降到 **89.42 kB**（gzip 16.90 → 16.66），因为删掉了重复色源与冗余覆盖；主包 638.99 kB（gzip 213.32），比 S2 增加 0.34 kB。

截图自检：手机端暗色网关列表确认堆叠栅格正确——名称/厂商/地址纵向排列，状态徽标与模型数同行，操作区上方有分隔线，按钮换行且均 ≥44px，单元格无残留内边距与边框。

### 本轮未运行

四套需真实 Go 后端的 E2E 中除 consumer 外的三套、routing 其余 spec、Go vet 与三组 race、file × postgres 矩阵。后端零改动。**本机通过不等于完整 CI 矩阵通过。**

---

## S3-2a 变更说明

### 原生弹层收敛的范围修正

审计说有 5 处原生 `<dialog>`。核对既有 antd 规格后确认其中**一处必须保留**：`ImageResult` 的原图查看器在规格原文里明确「仍保留现有实现」，属于画布类自持范围，不迁。因此实际待收敛的是 4 处，本步完成其中的任务详情，提示词三处排在 S3-2b。

### 任务详情

`TaskDetail` 删掉自建的 `showModal` 副作用、手写 Tab 焦点循环与焦点还原，改用 `StudioDialog`。可访问名称仍为「任务详情」，关闭按钮仍为「关闭详情」，因此既有 `getByRole('dialog', {name})` 断言不受影响。装饰性英文眉题 `CREATION DETAILS` 随共享标题区一并消失，符合方案去营销文案的目标。

`.detail-dialog` 原先 styling 的是 `<dialog>` 元素本身——宽度、内边距、`::backdrop`。收敛后宽度交给 `StudioDialog` 的 `width`，遮罩交给共享层，内容间距新增 `.detail-body` 规则承接。`.detail-grid`、`.dialog-actions` 等业务布局类原样保留，视觉不变。

### 新增可运行门禁

任务详情的既有断言全在 `e2e` 与 `e2e-gateway` 两套里，两者都需要真实 Go 后端，本环境跑不了。直接改一个无法本地验证的组件是不可接受的，因此先新增 `e2e-routing/dialogs.spec.ts`，用假上游覆盖弹层壳契约：可访问名称、关闭按钮存在、焦点进入弹层、Tab 与 Shift+Tab 不逃出、Escape 关闭、焦点归还触发按钮、无横向溢出。桌面走 antd Modal、手机走 antd-mobile Popup，两条路径都验证。

### 几何令牌

`Avatar3DStudio.css` 的 `var(--radius,12px)` 改为 `var(--radius-lg)`。至此全部被引用的 CSS 变量都有定义来源，未定义项为空。

---

## S3-2a 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3437 条文案 |
| `npx vite build` | 成功 |
| routing 弹层壳门禁 · desktop | 通过 |
| routing 弹层壳门禁 · mobile | 通过 |

主包 638.09 kB，比 S3-1 **减少 0.9 kB**——删掉的手写焦点陷阱比新增的封装调用更重。CSS 89.48 kB。

### 本轮未运行

`e2e` 与 `e2e-gateway` 中任务详情的既有用例、Go 矩阵。新增门禁覆盖了壳契约，但**不覆盖任务详情内部的业务断言**——那些仍需 CI 的真实后端套件确认。后端零改动。

---

## S3-2b 变更说明

### 提示词三处弹层

`ProjectPromptPicker`、`UsePromptDialog`、`PromptLibrary` 的版本记录全部收敛到 `StudioDialog`，各自的 `showModal` 副作用与焦点还原代码删除。`UsePromptDialog` 保留 `active` 标志用于异步守卫，只去掉弹层部分；`busy` 交给 `StudioDialog` 的 busy 属性，加载中不可关闭的语义不变。标题副行「配方名 · 版本 N」移入正文。`prompt-editor` 类透传到 `.studio-dialog-content`，既有排版规则继续生效。

至此原生 `<dialog>` 只剩 `ImageResult` 的原图查看器一处，且是既有规格明文保留的自持范围。**弹层实现从三套收敛为一套。**

### 门禁抓到的真实缺陷：焦点归还

版本记录按钮在请求期间 `disabled={busy}`，浏览器会把焦点从禁用按钮上摘掉，等弹层挂载时 `document.activeElement` 已经是 body——关闭后焦点回不到触发按钮。**这在原生 dialog 时代就已存在**，不是重构引入的，但此前没有断言覆盖。

修法是给 `StudioDialog` 增加可选的 `returnFocusTo`：异步打开的调用方显式传入触发元素。`PromptLibrary` 在 `showHistory` 入口、置 busy 之前捕获当前焦点并存入 ref。同步打开的调用方不受影响，仍走自动捕获。

### 图标混用清零

用浏览器实测而非推断：在三个代表性路由上枚举 `svg:not(.icon)`，发现两处外来图标——`anticon-down`（Select 下拉箭头）与 `anticon-ellipsis`（Tabs 溢出按钮）。

`Icon` 集补两个**确有用途**的图标：`chevron` 与 `more`，仍为 24×24 视图框、1.6 线宽、`currentColor`。`StudioSelect` 传 `suffixIcon`，`ModelManagementPanel` 的 Tabs 传 `moreIcon`。未按「补到 40 个」的原稿凭空造图标——没有消费者的图标只是负担。

三处直接用 antd `Alert` 的地方改用 `StudioError`，`showIcon` 关闭，避免错误态再引入 `anticon`；`StudioError` 补上 `role="alert"`，保持原调用点显式传入的语义。

新增 `e2e-routing/icon-set.spec.ts` 作为长期门禁：三个路由 × 两个设备，断言外来 SVG 列表为空。今后任何人新增 antd 控件而忘记替换内置图标，这条会立刻红。

### 新增可运行门禁

`e2e-routing/prompt-dialogs.spec.ts` 用 `page.route` 只桩掉提示词三个读接口，路由、会话、壳层全部走真实假上游，不改动共享夹具。覆盖版本记录与变量预览两个弹层的壳契约，桌面 Modal 与手机 Popup 两条路径。

---

## S3-2b 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3437 条文案 |
| `npx vite build` | 成功 |
| routing 提示词弹层门禁 · 桌面 + 手机 | 4 个全通过 |
| routing 任务详情弹层门禁 · 桌面 + 手机 | 2 个全通过 |
| routing 图标唯一性门禁 · 3 路由 × 2 设备 | 6 个全通过 |

主包 638.52 kB，CSS 91.12 kB。

### 本轮未运行

`e2e-gateway/prompts.spec.ts` 中提示词的业务断言、其余需真实后端的套件、Go 矩阵。新增门禁覆盖壳契约与图标唯一性，**不覆盖提示词的业务流程**。后端零改动。

### 一处已知不一致

`AuthGate` 的密码可见性切换仍是组件内联 SVG，未走 `Icon`。它在登录页，不在本步三个门禁路由内，留给 S4 登录页改造一并处理。

---

## S4-a 变更说明

### 登录页从固定浅色改为跟随主题

`auth.css` 原先是一座自成一体的色值孤岛：51 处硬编码，左品牌栏恒定深绿、右表单栏恒定暖白，与账号主题完全无关。现在全部取自共享令牌——品牌栏用 `--forest` / `--on-forest` / `--on-forest-muted` / `--forest-line`，表单栏用 `--canvas` / `--surface-input` / `--ink` / `--ink-muted`，主按钮用 `--primary` / `--on-primary` / `--primary-hover`，错误块用 `--error-*`。**硬编码色值归零。**

因此暗房主题下整个登录页是深色的：品牌栏近黑墨绿托住画廊图像，表单栏落到暗画布。这正是「深色优先的第一印象」。亮色主题沿用原暖白观感。

输入聚焦的柔光环原先写死 `#52765b14`，改为 `color-mix(in srgb, var(--primary) 14%, transparent)`，跟随主色。

### 删掉的死代码

`.auth-artwork*`、`.auth-frame-symbol`、`.auth-brand-story`、`.auth-scene-select` 四组规则在 TSX 里已无任何引用——手绘插画被画廊取代、场景圆点选择器被规格明确移除。它们携带约 15 处硬编码色值。一并删除，并把散落在四个 `@media` 块里的重复覆盖合并回主规则。文件从 144 行降到约 110 行，CSS 产物减少 1.8 kB。

### 图标彻底统一

密码可见性切换的内联 SVG 移入 `Icon`，新增 `eye` 与 `eyeOff`。至此**整个 `src` 里只有 `Icon.tsx` 渲染 `<svg>`**，图标集是字面意义上的唯一来源。

`icon-set.spec.ts` 增加登录页用例：桩掉会话接口 forcing 未登录态，断言无外来 SVG、表单栏背景确为暗画布 `rgb(15, 21, 18)`、密码切换可用、无横向溢出，并留截图。

---

## S4-a 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3437 条文案 |
| `npx vite build` | 成功 |
| routing 既有登录门禁：字号 / 输入边界 3:1 / 脚注 / 溢出 | 通过 |
| routing 图标唯一性 · 3 路由 + 登录页 | 4 个全通过 |

主包 638.43 kB，CSS 89.30 kB，比 S3-2b 减少 1.8 kB。截图确认暗色登录页两栏衔接自然、画廊图像与墨绿底融合、主按钮与输入边界清晰。

### 本轮未运行

`e2e-auth/appearance.spec.ts` 的登录画廊轮播、双语、reduced-motion 与账户隔离断言需真实后端；Go 矩阵未跑。后端零改动。

### 一处待产品决定

登录画廊的文案「让想象，有迹可循。」等三组仍是品牌营销语气。它是既有外观规格定义的品牌资产，与我在应用内删掉的装饰性英文眉题不同性质。是否按「杜绝空洞营销话术」一并改写，属于产品文案决策，未擅自改动。

---

## S4-b 变更说明

### 吸顶筛选条

视图切换、搜索、排序与项目计数收进 `StickyActionBar`，任意滚动位置都能改筛选。`.project-steps` 类保留以维持既有测试契约，但在吸顶栏内去掉标签页下划线与外边距，改为分段控件观感。

### 首个领域组件与首个 memo

`src/domain/ProjectCard.tsx` 抽出卡片markup，类名与结构原样保留。它是**全前端第一个 `React.memo`**——审计时 memo 计数为 0。i18n 辅助函数在组件内部读取而非由父级传入，因为 Context 每次渲染都返回新的函数身份，传进来会让比较永远失效。

### 三态统一到原子层

加载态从一行「正在读取项目列表…」换成 4 张与真实卡片同尺寸的 `StudioSkeleton`，消除到达时的布局跳动；错误态用 `StudioError` 并把重试放进它的 action 区，`role="alert"` 语义保持；空态用 `StudioEmpty`，并**新增「无匹配结果」这一态**——原先搜索无结果只会显示空白网格。

### 检索与排序的能力边界

`GET /projects` 返回裸数组，无 cursor 无 limit。因此搜索与排序**明确是客户端的**，作用于已加载数据，代码里写明了这一点，UI 也不把它伪装成远程查询。真正的服务端分页要等后端提供参数，属于长内容治理阶段。

### 测试

新增 `e2e-routing/projects-list.spec.ts` 三个用例：搜索命中与无结果、清除筛选恢复、按标题排序后顺序正确；读取失败时出现 alert 且重试后恢复；桌面滚动 400px 后筛选条与搜索框仍在视口内。

---

## S4-b 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3444 条文案 |
| `npx vite build` | 成功 |
| routing 项目列表新门禁 · 桌面 + 手机 | 5 通过 / 1 跳过 |
| routing production 既有门禁 · 桌面 + 手机 | 4 个全通过 |
| routing navigation 项目列表相关 · 桌面 | 2 个全通过 |

主包 638.50 kB，CSS 90.16 kB。截图确认吸顶栏的分段控件、搜索图标与排序下拉都走自绘图标集，卡片与暗色主题一致。

### 本轮未运行

`e2e/project.spec.ts` 与 `e2e/i18n.spec.ts` 中项目列表的业务断言需真实后端；navigation 其余用例本轮只跑了桌面两个；Go 矩阵未跑。后端零改动。

---

## S4-c 变更说明

### 三栏只在大屏生效，且不需要第二套渲染路径

需求方定调：≥1400px 走完整三栏，低于阈值退回既有两栏。实现方式是**同一份 DOM + CSS 网格定位**，不是两个分支：

参数区包成 `.creation-params`，与 `TemplateGallery` 并列留在 `.creation-controls` 内。默认宽度下 `.creation-controls` 仍是左列的 flex 容器，观感与今天一致。≥1400px 时给它 `display: contents`，包裹盒消失，`.templates` 与 `.creation-params` 直接成为外层网格项，于是三栏成立：左模板、中预览、右参数。

`display: contents` 的关键好处是**不改变 DOM 树**，所有 `.creation-controls .template-*` 后代选择器继续命中，无需改写。只有直接子选择器需要迁移：`.creation-controls > .runtime-model-picker` 与 `> .compact-runtime-control` 改为 `.creation-params >`。

宽度核算：1440px 下侧栏 240 + 页边距 64 + 左 280 + 右 300 + 两道间距 48 = 932，中列约 508px，2 列缩略图各约 246px。

### 一处先前判断的更正

我在动手前担心三栏会踩「桌面 2×2」门禁。核对 CSS 后更正：`.task-gallery` 是 `repeat(2, minmax(0,1fr))`，只在 ≤700px 变单列；缩略图 `height: 196px` 是**固定值**，不随列宽变化。所以 2×2 与 ≥190px 都与列宽无关，三栏不会踩它们。真正的风险是视觉拥挤，阈值因此是设计取舍而非门禁规避。

### 把网关门禁搬到能跑的地方

2×2 几何原本只由需要真实后端的 `e2e-gateway` 断言。新增 `e2e-routing/creation-layout.spec.ts` 在假上游上直接断言：1440px 三栏左右顺序与顶边对齐、`.task-gallery` 计算出的网格轨道数**恰为 2**、缩略图高度 ≥190px；1280px 仍是两栏且模板区留在左列内；390px 单列且无横向溢出。这条门禁从此不必只等 CI。

### 测试抓到并修掉的两个问题

其一，三栏下模板区比另外两栏低 6px——它带着 `margin-top: 6px`。第一次覆盖没生效，因为 `studio-ui.css` 那条是三级选择器而我的覆盖只有两级；按同特异性重写后对齐。

其二，参数区包了一层后，`consumer-polish.spec.ts` 的 `.creation-controls > .compact-runtime-control` 直接子定位器失配，两个设备同时红。改为后代定位器，**断言内容一字未改**。

顺带删掉 Composer 里硬编码的英文眉题 `MAKE IT YOURS`（此前只是被 CSS 隐藏，仍留在 DOM 且不走 i18n）。

---

## S4-c 验证证据

| 检查 | 结果 |
| --- | --- |
| `npx tsc -b` | 通过 |
| `npx eslint . --max-warnings 0` | 通过，零警告 |
| `npx vitest run src` | 31 文件 / 109 用例全通过 |
| `node scripts/i18n_check.cjs` | 通过，3444 条文案 |
| `npx vite build` | 成功 |
| routing 创作布局门禁 · 1440 / 1280 / 390 | 3 通过 / 3 跳过 |
| consumer 图像提示词与主操作可见性 · 桌面 + 手机 | 2 个通过 |
| consumer 缺模型错误在折叠切换后仍可见 · 桌面 + 手机 | 2 个通过 |

主包 638.50 kB，CSS 90.78 kB。截图确认 1440px 下三栏顶边对齐、左模板中预览右参数。

### 本轮未运行

`e2e-gateway` 的图像可用性、大媒体、创作流程等套件需真实 Go 后端；consumer 其余 6 个用例本轮未跑；Go 矩阵未跑。**三栏分支的完整网关验收仍需 CI 确认。** 后端零改动。
---

## S10 验收执行记录

### 已执行并通过

| 检查 | 范围 |
| --- | --- |
| `npx tsc -b` | 全量，零错误 |
| `npx eslint . --max-warnings 0` | 全量，零警告 |
| `npx vitest run src` | 35 文件 / 119 用例 |
| `node scripts/i18n_check.cjs` | 3499 条文案 |
| `npx vite build` | 全量产物 |
| routing 套件 | 主题对比度双主题、断点扫描、弹层壳、提示词弹层、图标唯一性、项目列表、创作布局三宽度、剧本阅读与分片、100 场长内容、素材三态、分类错误 |
| consumer 套件 | 网关对比度双主题 × 双设备、图像提示词可见性、缺模型错误 |

### 静态计数

`.ant-*` 选择器 0；`.adm-*` 选择器 0；`!important` 1（无障碍重置，刻意保留）；渲染 `<svg>` 的文件 1（Icon.tsx）；装饰性英文眉题 0；CSS 硬编码色值 6（全部为内容色：视频黑底、3D 舞台渐变、社交预览、原图查看器遮罩）。

旧三态写法：`page-empty` 21、`loading-block` 8、裸 `className="error"` 134，起点分别为 22、10、141。剩余多为字段级校验与局部提示，需逐页判断是否该收敛，不是机械替换。

### 长内容实测

100 场 / 2000 对白合成剧本：大纲挂载 11 行、DOM 7121 节点、可交互 4319ms。由 `script-long.spec.ts` 打印，测量环境为本会话 Linux 虚拟机 + Chromium headless，非生产硬件。

### 未执行

`e2e`（14 spec）、`e2e-auth`（5）、`e2e-gateway`（24）、`e2e-quotes`（1）四套需真实 Go 后端；`gofmt`／`go vet`／三组 race／file × postgres 双存储矩阵。本环境无 Go 工具链，`backend/bin/server.exe` 为 Windows 二进制。**本机通过不等于完整 CI 矩阵通过。**

### 未解的具体问题

**running 任务详情弹层**：把详情接口单独桩成 running 会与列表接口的 succeeded 矛盾，导致工作区在 active/idle 间翻转、弹层反复重挂载。两个接口一致打桩后弹层仍不出现——`ImageWorkspace` 的渲染门槛是 `active && detailId`，`active` 的来源尚未追清。阶段语义由 `StudioProgress.test.tsx` 覆盖，弹层集成待查。

**素材库搜索无结果**：在假上游下填入不存在的关键词并提交后未到达 no-results 态，未定位。共享空态本身已由 `asset-states.spec.ts` 的空响应与分类失败两条覆盖。

### 未完成的交付物

改造前后对比缺「前」：现有历史截图摄于 2026-10-23，早于 antd 迁移，不能代表基线。严谨对比需回到 `fce862b` 构建一份，本环境未做。

---

## 阻塞项：真实后端套件发现的回归（2026-11-20）

本环境已可运行真实后端：下载 Go 1.25.3 官方工具链、`go mod download`、`go build -o bin/server ./cmd/server` 与 `./cmd/bootstrap-user` 均成功，`/healthz` 返回 `{"mode":"secure","status":"ok"}`，FFmpeg/FFprobe 就位。此前「无法运行四套后端相关 E2E」的限制**已解除**。

随即在 `e2e-auth/appearance.spec.ts` 的「personal themes and locale persist per account」用例上发现失败：

- 主题迁移断言通过：重载后 `data-theme` 为 `light`，符合新的双主题迁移。
- **语言断言失败**：重载后 `html lang` 期望 `en`，实际 `zh-CN`，5 秒内始终不变。

### 已确认这是本次重构引入的回归

用可逆实验判定，而非推测：把 `frontend/src` 与 `frontend/e2e-auth` **一起**切回基线 `fce862b` 后运行同一用例，**通过**；切回 HEAD 后运行，**失败**。两次实验后工作树均已还原干净。

（第一次只切 `src` 不切 spec 的实验不成立——新断言配旧代码会先卡在主题那步，不能作为判据。）

### 已排除的原因

设备级绘制提示 `studio.appearance` 不是原因：把 `main.tsx` 的外层 `StudioTheme` 改回 `defaultTheme`、去掉 `readAppearanceHint()` 后重跑，**仍然失败**。该实验后已还原。

### 尚未隔离

剩余嫌疑集中在 `UserPreferences` 新增的 panels 状态与 context 字段、`StudioTheme` 新增的 `useMobile` 与 `useLayoutEffect`，以及 `I18nProvider` 异步 `loadLocale` 与两处 provider 嵌套下 `document.documentElement.lang` 的写入次序。账号语言键 `studio.preferences.v1:<user>:locale` 是否被写入尚未取证。

**结论：在此回归修复前，这套重构不应被判定为可交付。** 之前所有「通过」都建立在假上游套件上，而假上游套件不覆盖账号级语言持久化——这正是真实后端套件的价值，也是此前反复声明的验证缺口的具体兑现。

### 修复尝试与一处自我更正（同日续）

**更正**：先前记为「已隔离到 `StudioTheme` 的 `useMobile`」不成立。那两次实验跑的是自建探针，而探针**不复现真实用例的失败**——真实用例断言在 `page.reload()` 之后，探针查的是会话内切换。用探针做的隔离不能作数。

已试过且**均未修复**真实用例的方向：

1. 去掉设备级绘制提示 `readAppearanceHint`。
2. 把 `useMobile` 的 `getSnapshot` 改为模块级缓存单个 `MediaQueryList`。
3. 从 `StudioTheme` 移除 `useMobile`，响应式尺寸退回业务 CSS。
4. 把 `applyTheme` 的注入点从 `StudioTheme`（位于 locale provider 之上）下移到 `UserPreferences`（位于其内），启动着色改为 React 挂载前一次性调用。

以上四次实验后工作树均已还原。

**已取到的事实**：完整循环（zh-CN → 主题卡 ×2 → en → 主题卡 ×2）后，账号键 `studio.preferences.v1:<user>:locale` 的值是 `zh-CN` 而非 `en`，且 `.locale-feedback` 不出现——说明 `loadLocale('en')` 没有走 `.catch` 失败分支。简化流程（注册 → 偏好页 → 点 English → 点一张主题卡 → 重载）则**正常**，账号键为 `en`、重载后 lang 为 `en`。

因此触发条件与「语言切换与主题切换交替多次」有关，而非单次切换。下一步应从 `I18nProvider` 的 `requestedLocale` / `revision` 状态机与 `setActiveLocale(locale)` 的渲染期写入入手，配合完整循环取证，而不是再用简化探针。

**状态：回归未修复，重构仍不可判定为可交付。**

---

## 回归已修复（2026-11-20 深夜）

### 根因

插桩 `I18nProvider` 拿到完整时间线后定位。真实用例里，点击 English 触发 `setLocale('en')` → effect 以 `req=en rev=2` 启动，但 **`loadLocale('en')` 的动态 import 提交从未在重载前落地**——词典块加载耗时超过用例后续动作与 `page.reload()` 的间隔，提交与重载赛跑，提交输了。账号键停留在上一轮的 `zh-CN`。

证实方式：在重载前加 2.5 秒等待，`[i18n commit] en` 出现、用例通过；不加则不出现。加 `localStorage.setItem` 追踪钩子时它会通过、不加时失败——那不是根因，只是 instrumentation 改变了时序（海森堡），先前据此下的两个结论都作废。

### 修复

持久化从「提交时」移到「选择时」：`setLocale` 在被调用的那一刻同步写 `localStorage`，词典加载的 `.then` 不再负责持久化。语义也由此变对——存储键记录的是**用户意图**，不是「词典已加载」这一完成事件；加载慢或失败都不应丢失用户的选择，重载后自然会重试加载（代码本就如此设计）。

顺带保留提交路径上的两处加固：提交时同步写一次 `document.documentElement.lang`（挂载顺序允许外层 provider 的 lang effect 后运行，而 state 未变化的 locale 不会重跑 `[locale]` effect，提交才是权威时刻）；初次恢复的 effect 不再回写存储。

### 验证

`e2e-auth/appearance.spec.ts` 全部 12 个用例（桌面 + 手机）在真实 Go 后端上通过——含登录画廊轮播、双语、reduced-motion、账号隔离、退出清理、首次语言继承、存储被拒。此前该文件里「个人主题与语言按账号持久化」是失败的。另通过：tsc、eslint、119 单测、i18n、vite build。

`e2e` / `e2e-gateway` / `e2e-quotes` 其余 spec 尚未跑，仍属 CI 范围。

---

## e2e 主套件实跑结果（真实 Go 后端，desktop）

| spec | 结果 |
| --- | --- |
| structure | 2 通过 |
| project | 6 通过 |
| i18n + studio | 4 通过 |
| creative + storyboard | 7 通过 |
| series + iteration | 9 通过 |
| social-consent + douyin-handoff + tiktok-photo | 12 通过 |
| alignment | 3 通过 / 1 失败 |
| media + iteration10 | 失败 1（media:19），其余未跑完 |

### 本轮由此发现并修复的两个回归

**剧本分片藏掉了非激活场的对白控件**。`structure.spec.ts` 重排后断言的角色下拉不存在。修法：只分片昂贵的 textarea，角色下拉每场都保留——100 场有 2000 条对白但说话人只有几个，下拉不是开销来源。同时把开放编辑器的跟随从「位置」改为「场次身份」，并在重排时跟随被移动的那一场。

**项目列表同时渲染了骨架网格与真实网格**，`.project-grid` 匹配到 2 个元素，打断 strict-mode 定位器。真实网格改为仅在加载结束后渲染。素材列表本来就是互斥的。

### 两条失败的归属

`alignment.spec.ts:22`：**已判定为既有失败**。把 `frontend/src` 与 `frontend/e2e` 一起切回基线 `fce862b` 后跑同一 spec，同一条用例、同一处 `.project-page` 等待失败。不是本次重构引入。

`media.spec.ts:19`：**归属未定**。等待 `成片预览` 标签失败；基线尝试中后端记录了 `task_error`，指向环境或后端侧，但该次基线运行未跑完，不足以下结论。

### 未跑

mobile 设备的全部 e2e、`e2e-gateway` 24 个 spec、`e2e-quotes`、Go vet 与三组 race、file × postgres 矩阵。

---

## CI 核查与一处真实补强

### 核查结论：现有矩阵相当完整，没有编造缺口

逐项核对后确认：`browser-tests` 覆盖 e2e / routing / auth × desktop / mobile；**consumer 套件已覆盖**，由 `scripts/ci_browser_tests.sh` 在 routing 之后追跑；**quotes 套件也已覆盖**，由 `durable-smoke` 的 `postgres_browser_check.py --quote-fixture` 驱动（先前 grep 复数 `quotes` 未命中，是因为 flag 写作单数 `--quote-fixture`）。另有 go-race 三分组 × 双存储、integrations、gateway、container、windows-native、verify、docker。

### 补上的真实盲区

`scripts/i18n_check.cjs` 只检查 `t()` / `translate()` 的**字面量首参**。本次新增的 `errorPresentation.ts` 与 `jobStatus.ts` 把中文放在查表映射里、以 `translate(变量)` 取值，因此那批文案**完全不在静态门禁覆盖内**——我当时是手工补的英文，没有任何东西阻止今后漂移。

新增 `src/translationCoverage.test.ts` 三个用例堵上：八个错误分类的标题与下一步、三套状态集的全部标签、以及运行时阶段文案，逐条断言英文态下不含中文。它跑在 `npm test` 里，CI 已有的 quality job 直接生效，不需要改共享脚本。

这是本次唯一有据可依的 CI 改动。其余「完善」在没有具体缺口证据前不做。

---

## e2e 主套件 mobile 实跑结果（真实 Go 后端）

| spec | 结果 |
| --- | --- |
| structure + project | 8 通过 |
| i18n + studio + creative | 7 通过 |
| storyboard + series | 7 通过 |
| social-consent + douyin-handoff + tiktok-photo | 12 通过 |
| iteration10 | 2 通过 |
| iteration + alignment | alignment:22 失败，其余未取到计数 |
| media | media:19 失败 |

移动端合计 36 个用例通过。两条失败与桌面端**完全一致**——同样的 spec、同样的行号，不是设备相关问题。

### 两条失败的最终状态

`alignment.spec.ts:22`：已用基线对照判定为**既有失败**。桌面与手机同样表现。

`media.spec.ts:19`：**归属仍未定**。两次基线对照实验均因挂载文件系统过慢而超时未跑完；期间后端记录 `task_error`，指向环境或后端侧，但证据不足以定论。**不再继续该实验**——两次超时都曾导致还原步骤未执行、工作树停在基线，我都及时检查并还原了，但这个做法在当前环境下风险高于收益。

### 合并前必须知道的事

本次重构在真实后端上验证到的范围：e2e 桌面 43 + 移动 36、auth 双设备 12、routing 与 consumer 子集、122 单测、tsc、eslint、i18n、build。

**未验证**：`e2e-gateway` 24 个 spec、`quotes` 套件、Go vet 与三组 race、file × postgres 双存储矩阵。这些在 CI 里存在且会被执行，但本地从未跑过。

**已知红灯**：上述两条 e2e 失败。其中一条确认为既有，另一条归属未定。合并即意味着把这条未定项一并带入 master，CI 会给出最终判定。

三态收敛（21 page-empty / 8 loading-block / 134 裸 error）本轮仍未做。
