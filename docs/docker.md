# 当前安全模式与本轮验证边界

默认安全模式不开放注册，cookie需要HTTPS。仅本机HTTP演练时，显式设置 `AUTH_ALLOW_REGISTRATION=true AUTH_COOKIE_SECURE=false docker compose up -d`。不要将这个HTTP设置用于公网部署。2026-10-26已获准推送master和既有CI镜像发布，未授权应用部署。本地基础镜像拉取曾受DNS阻塞；提交`d9a6954a4906cd615a8b7a5e9227e5f4809936a2`的[CI双容器验收](https://github.com/adcwb/ai-creative-studio/actions/runs/40271837020)实际通过。该次运行因managed eval脚本缺Go缓存默认路径而未发布镜像，修复保持凭据隔离和全部断言；后续发布结果以精确SHA的全部CI任务及镜像digest为准。

compose使用专用网段（默认172.30.89.0/24）与前端固定IP（默认172.30.89.3），后端只信任该IP的X-Real-IP；Nginx覆盖客户端该header，以免所有用户共用代理IP的登录速率限制。若已有网段冲突，同时显式更改STUDIO_INTERNAL_SUBNET与其中的STUDIO_FRONTEND_IP。禁止把信任范围扩大为全部公网或直接相信X-Forwarded-For。代理访问日志不含query，以免记录OIDC code/state。

下方包含上一轮发布与运行记录；本轮授权以上方2026-10-26说明为准。最新能力和缺口见[后续交付矩阵](iteration-9-followup.md)。

---

# Docker 运行与镜像发布

Dockerfile 和 Compose 提供本地运行方式；CI 构建/发布镜像不等于应用部署。本次允许在本地验证后推送默认分支并由 CI 发布已确认的两个镜像仓库。构建和发布的实际结果以 CI 与本轮验证报告为准，本文件不提前宣称成功。

## 本地启动

在仓库根目录使用 Docker Engine 和 Compose v2：

```bash
docker compose build
docker compose up -d
docker compose ps
```

浏览器访问 **http://localhost:8080**。默认仅前端容器映射到宿主机 `127.0.0.1:8080`；后端没有宿主端口，通过 Compose 内部网络由 Nginx 访问。默认使用安全认证模式；仍不应把回环绑定直接改成公网监听，生产TLS、注册与外部服务配置须单独验收。

自定义本地端口时，同步设置浏览器实际来源：

```bash
STUDIO_PORT=8090 FRONTEND_ORIGIN=http://localhost:8090 docker compose up -d
```

CORS 放行所有来源，并通过回显请求 `Origin` 支持携带凭据；写请求仍要求非空 `Origin`，登录后的写请求另要求 `X-CSRF-Token`。`FRONTEND_ORIGIN` 仅用于 SSO 登录完成后的固定回跳，启用 SSO 时应配置为实际前端地址。会话 Cookie 仍为 `SameSite=Lax`，浏览器的跨站 Cookie 限制仍适用。后端环境只从 Compose 明确列出的配置读取；宿主 `backend/.env` 不会自动进入容器。默认模拟图像/文本，无真实语音、视频或ASR模型调用；上传素材、本地时间轴和明确标识的合成演示仍可使用。

检查运行状态与停止服务：

```bash
docker compose logs --tail=100 backend frontend
curl --fail http://localhost:8080/healthz
curl --fail http://localhost:8080/readyz
docker compose down
```

`/readyz` 检查前端 Nginx，`/healthz` 经代理检查后端。正常 `down` 保留命名数据卷；不要在需要保留项目、历史、素材或成片时添加 `-v`。

## 镜像边界

| 组件 | 构建上下文 | 运行内容与身份 |
| --- | --- | --- |
| 后端 | `./backend`，`backend/Dockerfile` | Go构建阶段；运行镜像含FFmpeg/ffprobe、CA证书和Noto中文字体；UID/GID `10001:10001`，数据目录`/app/data` |
| 前端 | `./frontend`，`frontend/Dockerfile` | Node24构建阶段；Nginx以`nginx`用户监听8080，临时文件写入`/tmp` |

前端构建固定 `VITE_API_BASE_URL=/`，代码将其规范为同源地址；Nginx把`/api/`和`/healthz`代理到`backend:8080`，其余请求提供SPA。该值在构建时生效，不能靠启动后设置浏览器环境变量改写。Nginx的33MiB请求上限只是入口限制，后端仍执行32MiB上传和格式/像素/时长校验。

Compose 使用 `studio-data` 命名卷保存完整`/app/data`；Docker实际卷名会包含Compose项目名。后端文件锁仍限制单进程写入，不应把同一卷挂给多个后端副本。备份应先停服并备份整个卷，包括主状态、创作/媒体/字幕任务、历史、原素材及成片；恢复时保持UID10001的读写权限。容器重建不会自动迁移到数据库或完成生产身份与外部服务配置。

两个`.dockerignore`均采用文件白名单，排除`.env`、运行数据、测试证据、构建产物和仓库其他文件。不要把模型或Docker Hub凭据写入Dockerfile、镜像构建参数或Git；需要真实模型时应另行采用受保护的运行配置，并确认模型与费用授权。

## CI 与 Docker Hub

工作流`.github/workflows/ci.yml`对所有分支push及pull request执行。`container-smoke`与`verify`并行，构建并验证双容器代理、持久化和合成媒体；`docker`发布矩阵依赖`verify`、`container-smoke`和`durable-smoke`全部成功，分别以`./backend`与`./frontend`构建`linux/amd64`镜像。

- Pull request仅构建，不登录Docker Hub、不发布；fork仓库不发布。
- 已配置凭据的上游`adcwb/ai-creative-studio`分支push可以发布；默认分支还更新`latest`。
- 构建产物为两个独立镜像，默认仓库名`ai-creative-studio-backend`与`ai-creative-studio-frontend`。
- namespace默认使用Docker Hub用户名（兼容`vars.DOCKERHUB_USERNAME`，本次用户名保存在Secrets）；`DOCKERHUB_NAMESPACE`仓库变量可显式覆盖，例如组织namespace。覆盖值必须属于该凭据获准发布的目标。

GitHub Actions配置项（不在聊天、日志或代码中读取/展示实际值）：

| 位置 | 名称 | 用途 |
| --- | --- | --- |
| Actions **Secrets** | `DOCKERHUB_USERNAME` | Docker Hub登录用户名；默认namespace来源 |
| Actions **Secrets** | `DOCKERHUB_TOKEN` | Docker Hub登录token，仅从Secrets读取 |
| Actions Variables，可选 | `DOCKERHUB_USERNAME` | 兼容公开用户名配置；若设置会优先于同名Secret，本次无需新增 |
| Actions Variables，可选 | `DOCKERHUB_NAMESPACE` | 显式覆盖目标namespace |
| Actions Variables，可选 | `DOCKERHUB_BACKEND_REPOSITORY` | 覆盖后端仓库名，不含registry/namespace |
| Actions Variables，可选 | `DOCKERHUB_FRONTEND_REPOSITORY` | 覆盖前端仓库名，不含registry/namespace |

如果token曾误存入Variables，需要用户在GitHub设置中手动迁入Secrets并移除错误变量；不要把变量值复制到聊天或脚本输出。工作流不会回退读取`vars.DOCKERHUB_TOKEN`。namespace/repository只决定发布目标，不自动创建模型账户、应用部署或公网服务。

标签规则由metadata action生成：

| 标签 | 生成条件与用途 |
| --- | --- |
| `branch-<分支名>` | 分支push；分支名由metadata action规范化，是可变标签 |
| `sha-<完整提交SHA>` | 提交对应镜像；用于定位确切源码版本 |
| `latest` | 仅默认分支push；不用于其他分支或PR |
| `pr-<编号>` | PR本地构建标签；不推送到Docker Hub |

同一分支的新提交会取消旧工作流；取消时可能只有一个镜像已完成，因此应核对同一SHA的两个镜像，不能把部分发布当成完整交付。检查或容器验收失败不会绕过依赖发布。查看Actions结果时应同时核对verify、两个docker任务及镜像digest。仅看到源码push或标签计算步骤成功，不能视为镜像发布成功。

本次公开合成演示须单独标注来源，不能把QA trace、真实运行数据、凭据或合成结果当作真实AI验证一并发布。镜像发布不代表已部署网页，也不验证供应商认证、效果或费用。

CI的媒体播放回归使用Playwright官方Chrome channel，安装Chrome后运行原有完整断言。Playwright捆绑的开源Chromium并不包含所有H264/AAC媒体解码能力；此前CI的两项播放测试未加载视频，切换到具备相应解码能力的浏览器重新验证，不能通过跳过播放断言替代。参见 https://playwright.dev/docs/browsers#media-codecs 。本地可继续使用已经验证具备媒体解码能力的系统Chromium路径。
