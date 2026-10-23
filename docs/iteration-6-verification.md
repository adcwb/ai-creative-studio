# 第六轮第一阶段独立验证记录

基线 `2ff0b9260471d3ac6b248ee8ce07e16b4cdf9447`，第一阶段实现验收基于 `08efbb601dd667d1af3d0c8955c3875ab1fc1c4f`，2026-10-23。测试只使用本地假上游、程序合成媒体和隔离临时数据目录；没有配置真实凭据、调用付费模型、部署或推送。真实模型效果、供应商权限和费用均未验证。

本文件保留已实际执行的第一阶段快照。随后新增MiniMax图像/视频、Google Interactions图像/语音/视频、独立字节TTS、图片理解、逐句字幕与ASR候选、即梦持久任务恢复等能力；下述48项E2E与日志**不能代表这些后续修改已通过最终验收**。后续完整检查和交付证据由[追加验证记录](iteration-6-followup-verification.md)记录，运行结果以该报告为准。

## 第一阶段结果

| 检查 | 实际结果与证据 |
| --- | --- |
| Go race | `go test -race -count=1 ./...` 通过，studio 19.324s；94项顶层测试声明，专用 `TestProviderHarness` 默认跳过，由独立provider脚本启用；日志 `/tmp/iteration6-final-go.log` |
| Go vet / gofmt / build | 全部通过；二进制随后用于最终smoke、媒体和完整E2E |
| 前端 lint / typecheck / unit / build | 全部通过；4文件14单测；51模块构建成功 |
| OpenAPI结构及实际HTTP合同 | 通过；434个实际响应（包括轮询，非434独立用例），覆盖原图像、剧本、分镜及新历史、分析、供应商目录；`/tmp/iteration6-final-contract.log` |
| 原进程恢复smoke | 通过；图像/剧本/分镜真实SIGKILL恢复、来源过期、失败保稿、跨重启幂等；`/tmp/iteration6-final-smoke.log` |
| 媒体HTTP与实际编码导出 | 通过；28个MediaState/MediaExport合同响应；上传内容伪装与32MiB上限、幂等、版本/空间隔离、取消、SIGKILL后不重发；`/tmp/iteration6-final-media.log` |
| 完整桌面/手机E2E | **48/48通过（3.7分钟，退出码0）**；桌面24、手机24，串行workers=1，无重试或断言放宽；`/tmp/iteration6-final-e2e.log` |
| 原图像布局 | 24状态通过；`/tmp/studio-layout-evidence-U5T6qQ` |
| 原剧本布局 | 30状态通过；`/tmp/studio-project-evidence-WrlSx8` |
| 原分镜布局 | 36状态通过；`/tmp/studio-storyboard-evidence-16lQy6` |
| 原图像provider联调 | 通过；5次明确本地假上游调用，自动重放0次；`/tmp/studio-provider-evidence-UvZIiu` |
| 远端CI | 本轮未运行、未推送；不能将基线CI成功当成本轮成功 |

Go覆盖原流程、原图像协议以及新增OpenAI文本/语音/视频/参考图编辑、Claude/Gemini/MiniMax原生文本、Gemini/Ark图像与参考图、Ark视频、MiniMax语音、即梦签名图像协议；上游全部为httptest/合成响应。覆盖失败/超时/重定向/不安全URL与输出、无自动POST重试、持久请求回执、模型变更保护、真实模式缺凭据不回退模拟，以及已提交视频只查询原上游ID。代码支持不代表外部认证、模型质量或收费验证。

## 可复现的合成演示

最终本地媒体原始证据目录：`/tmp/studio-media-evidence-wjxgr8xi`。可交付副本在 `/workspace/ai-creative-studio-artifacts/iteration-6/`（独立于Git仓库，含合成MP4、SRT、探测与验收记录）。

- `synthetic-demo.mp4`：真正编码的MP4；三个合成色块镜头，带明确测试字幕与正弦测试音，不是真实AI画面或真人配音。
- `synthetic-demo.srt`：独立字幕文件。
- `ffprobe.json`：H.264 640×360视频（6.000秒）、48kHz AAC音轨（6.005秒）、mov_text字幕（6.000秒）；容器时长6.021秒，对齐6秒时间轴，误差小于0.25秒。
- `audio-volume.txt`：FFmpeg完整解码与音轨能量检查；mean -47.7dB、max -44.2dB，存在非静音合成信号。
- `acceptance.json`：实际通过项目、合同响应数以及 `model_validation:false`。

`scripts/media_check.py` 使用随机回环端口和新DATA_DIR，不读取开发数据或真实凭据；最后清理服务进程，保留合成证据供复核。MP4还经过浏览器实际播放检查：`currentTime`推进、视频尺寸有效、无媒体错误，中文WebVTT字幕轨有cue且为showing，下载链接及刷新后持久化均检查。

## 新增浏览器验收

`frontend/e2e/media.spec.ts` 在桌面和手机覆盖：

- 持久项目→mock剧本→mock分镜→分镜ID对应的合成素材→编辑时间轴字幕→本地合成→实际播放/字幕/下载→刷新后保留。
- 伪造PNG上传拒绝，素材库保持空。
- 另一客户端先保存后，即使轮询已读到新版本，旧草稿仍返回409并保留；响应丢失以同一个body/key重放，版本只增加一次。

`frontend/e2e/creative.spec.ts` 覆盖异步规则分析及合成图像任务、照片80×40像素裁剪/旋转并上传为40×40新资产、角色元数据、镜头增删排序和真实历史恢复。两组共12项新增E2E，与原36项一起完整运行并通过。最终完整运行期间没有改动应用代码或断言；随后仅完成验证记录。最终桌面 `/tmp/iteration6-media-desktop.png` 与手机 `/tmp/iteration6-media-mobile.png` 已实际查看，包含持久剧本、分镜、媒体、字幕与可播放成片，无中期404告警或水平溢出。最后测试选择器改动后又单独复跑lint，通过；`git diff --check`通过。

## 发现与修复

- 媒体返回对象与内存共享切片，race运行在 `TestMediaReplayVersionAndIsolation` 发现 caller mutation；改为提交时隔离反序列化，最终全Go race通过。
- 时间轴轮询若更新编辑版本可能绕过冲突检查；前端改为保留草稿基线版本，新增E2E证明冲突仍拒绝且保留输入。
- 新增VTT字段和图像能力reason code最初未同步契约；实际HTTP schema校验拒绝后补齐，未放宽additionalProperties。
- 中期第一次完整42项E2E发现创作HTTP路由未注册，404额外告警使旧严格选择器冲突；已中止（退出130）。主集成修复路由注册后重新构建，最终重跑48项，不将中期局部结果拼成全量成功。
- 最后界面文案统一后出现两个“关联角色”控件；照片测试限定`.media-studio`内的控件，保留原上传角色断言，不改产品行为。
- 独立审查还发现真实分析缺凭据可能回退模拟、已排队任务同provider更换model可能误调用。主集成加ready与持久model守卫、前端提交expected_model；最终race包含这些失败关闭测试。

## 工具环境与边界

Go1.25.3：`PATH=/tmp/studio-go125/go/bin:$PATH GOPATH=/tmp/studio-gopath GOCACHE=/tmp/studio-gocache`。npm依赖通过 `npm ci --cache /tmp/studio-npm-cache` 安装，默认缓存不可写的初次失败不涉及权限放宽或锁文件修改。Chromium `/usr/bin/chromium`；FFmpeg/ffprobe7.1.5；OpenAPI验证器0.7.2在 `/tmp/studio-validation`。ESLint9停止支持提示已记录，不阻碍本轮检查。

真实模型效果/费用、跨镜头身份一致性、账号权限、生产鉴权与多实例未验证。角色/镜头上传关联为元数据；供应商参考图能力有限，不能保证人物恒定。语音/视频默认未配置时保持不可用，可上传本地素材完成成片；正弦测试音不冒充配音。MVP是顺序拼接、裁剪、单段配音/音效、背景音乐和字幕，不是专业多轨剪辑软件。
