# 映创 · 设计系统

基线：分支 `refactor/workflow-structure`，2026-11-20
状态：随 S1–S9 落地，S10 收口中
配套：`docs/brand.md`（品牌与对比度口径）、`docs/superpowers/specs/2026-11-19-studio-ui-refactor/`（诊断与方案）、`docs/superpowers/plans/2026-11-19-studio-ui-refactor-plan.md`（每步验证证据）

## 1. 色值只有一个来源

`frontend/src/ui/palettes.ts` 是全站唯一色值定义。它同时派生 antd 的 `ConfigProvider` token 与运行时注入的 CSS 自定义属性，camelCase 键自动映射为既有变量名，没有手工对照表。**不要在 CSS 或组件里写第二个色值。**

主题两套：**暗房**（默认）与**工作室**。旧 forest / paper / screening 已下线，迁移为 forest、paper → 工作室，screening → 暗房，新账号默认暗房。

| 令牌 | 暗房 | 工作室 | 用途 |
| --- | --- | --- | --- |
| canvas | `#0f1512` | `#f7f7f2` | 页面底 |
| surface | `#171f1b` | `#ffffff` | 面板 |
| surfaceSoft | `#1e2823` | `#f0f3eb` | 次级块 |
| surfaceInput | `#131a17` | `#fbfcf8` | 输入底 |
| ink | `#e9f0ea` | `#263e32` | 正文 |
| inkMuted | `#a3b5aa` | `#63705e` | 次级文字 |
| inkPlaceholder | `#8c9e93` | `#63705e` | 占位 |
| border | `#2c3a34` | `#e0e5d9` | 装饰分隔 |
| borderStrong | `#4a5c54` | `#c5d0b8` | 强调分隔 |
| controlBorder | `#66796f` | `#7f8d78` | 控件边界 |
| forest | `#0a100d` | `#193b2e` | 侧栏轨道 |
| forestRaised | `#1b2620` | `#254637` | 侧栏抬起 |
| forestLine | `#2a3831` | `#3b5846` | 侧栏分隔 |
| onForest | `#e9f0ea` | `#f5f5ea` | 侧栏文字 |
| onForestMuted | `#a3b5aa` | `#b2c2aa` | 侧栏次级 |
| primary | `#2f7d5b` | `#244b3b` | 主操作 |
| primaryHover | `#2a7152` | `#35604a` | 悬停 |
| primaryActive | `#246347` | `#1c3b2e` | 按下 |
| onPrimary | `#ffffff` | `#ffffff` | 主色上的字 |
| link | `#7fc9a3` | `#3f6149` | 正文链接 |
| greenSoft | `#7fc9a3` | `#617b50` | 选中边框 |
| accent | `#cfe3a8` | `#d9e4b5` | 侧栏选中字 |
| rail | `#cfe3a8` | `#617b50` | 映窗光轨 |
| focus | `#e3b457` | `#a97f2a` | 焦点环 |
| scrim | `rgb(0 0 0 / 60%)` | `rgb(8 24 17 / 40%)` | 遮罩 |
| success | `#1b3327` / `#9fd8b4` | `#edf3e5` / `#4d6940` | 底 / 字 |
| warning | `#332c18` / `#e8ce93` | `#f6f2e7` / `#7c6940` | 底 / 字 |
| error | `#3a2422` / `#f2b3a7` | `#fff1ec` / `#a54837` | 底 / 字 |

**暗房主色悬停加深而非提亮**：提亮会让白字对比度掉到 4.39，低于 4.5 门槛。antd 暗色算法默认提亮，因此 `Button` 悬停色由 token 显式覆盖。

## 2. 非颜色令牌

`frontend/src/tokens.css` 只放这些，不放颜色。

| 组 | 值 |
| --- | --- |
| 字号 | 11 / 12 / 13 / 14 / 15 / 16 / 18 / 20 / 24 / 28px |
| 界面基线 | 14px，行高 1.6 |
| 阅读正文 | 16px，行高 1.8 |
| 间距 | 4 / 8 / 12 / 16 / 20 / 24 / 32px |
| 圆角 | 8 / 10 / 14px；胶囊 999px |
| 顶栏高度 | `--shell-topbar-height` 68px，760px 断点重定义为 60px |
| 工作台 | 大纲 260 / 检查器 320 / 收起轨 48 / 阅读限宽 760px |
| 层级 | sticky 20 < dock 24 < 遮罩 29 < 导航 30 < 弹层 1000 |

吸顶层的偏移全部由 `--shell-topbar-height` 派生，断点处重定义一次即可，不需要第二套数字。

## 3. 组件分层

页面层只允许调用封装层。

**原子层 `src/ui/`**

| 组件 | 底座 | 职责 |
| --- | --- | --- |
| StudioButton | antd Button | 主/次/危险/文字四档 |
| StudioCard | 业务 section.panel | 面板与统一标题区 |
| StudioTag | antd Tag | 状态与分类徽标 |
| StudioField | 业务 | 标签＋说明＋校验接线 |
| StudioInput / StudioTextArea | antd Input | 尺寸与错误态 |
| StudioSelect | antd Select | 桌面选择器，自绘箭头 |
| StudioDialog | Modal / Popup | 唯一弹层实现 |
| StudioEmpty | 业务 | 无数据 / 无结果 / 无权限 |
| StudioSkeleton | antd Skeleton | 文本 / 卡片 / 媒体三型 |
| StudioError | antd Alert | 分类指导＋诊断编号 |
| StudioProgress | 业务 | 三段阶段光轨 |
| StudioTheme | ConfigProvider | 唯一主题入口 |
| StudioWorkbench | 业务 | 三栏插槽 |
| StickyActionBar / NextStepDock | 业务 | 吸顶与底部推进 |

`StudioCard` 保持业务 `section.panel` 而非 antd Card：60 个调用点依赖 `.panel` 布局契约，换底属于后续迁移。

**领域层 `src/domain/`**：ProjectCard（全仓第一个 `React.memo`）、ParamSnapshot。

**长内容层 `src/reading/`**：ReadingCanvas、ReadingOutline、SceneNavigator、FindInPage、useReadingPosition。

## 4. 硬性规则

**禁止覆盖库类名。** CSS 中 `.ant-*` 与 `.adm-*` 选择器计数为 0。组件差异走三条路：`theme.components` token（含随 `useMobile()` 切换的响应式 token）、antd 6 语义 `classNames`（`Modal.container`、`Select.input`、`Tabs.item`、`Popup.maskClassName`）、业务类或元素选择器。与库默认值竞争的规则加 `.user-workspace` 前缀，由特异性而非源码顺序决胜。

**`!important` 只剩一处**，是 `prefers-reduced-motion` 重置。去掉它会让组件与库动画重新胜出，等于拿无障碍换风格约束。这是刻意保留的例外，已在 CSS 就地注明。

**图标只有一个来源。** 整个 `src` 里只有 `components/Icon.tsx` 渲染 `<svg>`，24×24 视图框、1.6 线宽、`currentColor`。库控件的内置图标必须显式替换：Select 的 `suffixIcon`、Tabs 的 `moreIcon`、Modal 的关闭按钮。`e2e-routing/icon-set.spec.ts` 在三个路由加登录页 × 两个设备上断言外来 SVG 列表为空。

**界面色与内容色分开。** 界面色一律令牌化。内容色不跟主题变：视频letterbox 黑底、3D 舞台渐变、社交平台预览底色、原图查看器遮罩。当前 CSS 中剩余 6 处硬编码色值全部属于这一类，是有意保留而非欠账。

## 5. 长内容约定

条目 ≤ 30 直接渲染，> 30 走 react-virtuoso。阈值集中在 `virtualThreshold` 一处。虚拟化用在**大纲**这类均匀长列表；正文保持完整 DOM 靠分片编辑降开销，这样浏览器原生查找与深链锚点仍可用。

页内搜索搜**数据**不搜 DOM——离屏行没挂载，搜 DOM 搜不到。当前场景写进 `?scene=`，节流 replace 写入，刷新与分享都能恢复。

编辑态只挂载当前激活场景的文本编辑器；结构按钮保留在每一场，因为重排不该需要先切模式。

## 6. 对比度门禁

`src/ui/palettes.test.ts` 在每次单测中强制：两套主题各 18 组文字配对 ≥ 4.5:1、18 组图形配对 ≥ 3:1，另断言色板键完整、变量名映射正确、暗房悬停必须加深。改色破线会直接测试失败，而不是等浏览器门禁。

浏览器侧由 `e2e-routing/design-system.spec.ts` 覆盖 tab 文字、输入边界、说明文字、主按钮默认与悬停、侧栏控件边界与焦点环、导航触控 ≥44px、无横向溢出。

## 7. 验证边界

已在本环境运行：tsc、eslint、119 个单测、i18n 静态覆盖、vite build，以及 routing 与 consumer 两套假上游浏览器套件的相关用例。

**未运行**：`e2e`、`e2e-auth`、`e2e-gateway`、`e2e-quotes` 四套需真实 Go 后端；全部 Go 测试与 file × postgres 双存储矩阵。本环境无 Go 工具链。所有「通过」的准确含义是上述范围，**不等于完整 CI 矩阵通过**。

长内容实测数据：100 场 / 2000 对白合成剧本下，大纲挂载 11 行、DOM 7121 节点、可交互 4319ms（本机，由 `script-long.spec.ts` 打印）。