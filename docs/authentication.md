# 第七轮身份、组织与OIDC

默认 `AUTH_MODE=secure`，所有创作、素材和下载接口要求登录；`/healthz`、`/api/v1/auth/session` 无需登录，登录注册与SSO入口有独立检查。`AUTH_MODE=demo` 明确保留无身份演示模式，不能作为多租户安全边界。新账号只拥有随机 `org-…` 工作空间，不能认领旧 `personal-demo` / `team-demo` 数据。

## 配置与边界

- `AUTH_ALLOW_REGISTRATION=true` 才开放自助注册（含首次SSO账号），默认关闭。不创建默认密码，不配置已有账号。首次使用需操作者明确开启注册，让用户自行注册后可关闭。
- `AUTH_COOKIE_SECURE` 默认true；仅HTTP回环开发显式设false。会话cookie为HttpOnly、SameSite=Lax、12小时；前端请求携带credentials。
- 所有写操作要求与 `FRONTEND_ORIGIN` 精确匹配的Origin；登录后写操作另要求 `X-CSRF-Token`。没有用客户端身份头、转发头、邮箱或space_id自称授权。
- 登录/注册每来源连接IP每15分钟20次、最多1024个限流桶。默认不信任转发头。部署可明确设置 `AUTH_TRUSTED_PROXY_CIDRS`（逗号分隔CIDR，最多32项），仅当TCP对端在该列表才使用其覆盖写入的 `X-Real-IP`；不读取X-Forwarded-For，非法地址拒绝。Nginx必须用 `$remote_addr` 覆盖X-Real-IP，不能传递客户端原头。Docker配置仅信任专用内部网络中的前端代理单IP；额外公网代理链须独立审查。没有分布式限流。每用户最多20个未过期session。
- 密码为12–128字节，独立随机16字节盐，PBKDF2-HMAC-SHA256 600000次、32字节派生值；session只存SHA256摘要。原密码和原session token不落盘。
- 暂无本地账号邮件验证、密码重置、MFA、其他设备退出及owner转移。自助注册不证明邮箱所有权，因此绝不自动以同邮箱绑定SSO。

## API与权限

`GET /api/v1/auth/session` 返回mode、authenticated、registration_enabled、sso_available；已登录时另含user `{id,email}`、csrf_token、spaces。`POST /api/v1/auth/register`、`/login` 接受 `{email,password}` 并设置会话cookie；`POST /api/v1/auth/logout` 撤销当前session。

`GET /api/v1/spaces` 只列当前用户的组织。`POST /api/v1/organizations` 接受 `{name}` 创建组织并成为owner。`GET /api/v1/organizations/{id}/members` 和 `PUT`（`{email,role}`，role为admin/editor/viewer/remove）仅owner/admin可用，目标须为已注册用户；不发送邮件或邀请。目标为SSO账号时必须同时提供其稳定 `user_id` 与当前邮箱匹配，管理员通过用户主动分享ID确认身份，不能仅依赖可能复用的邮箱授权；已有成员UI自动附带记录ID。admin不能修改owner/admin，owner不能被降级或删除。每个owner最多20组织，每组织200成员。

viewer只读，editor可修改创作内容，admin/owner另能管理成员。space_id只是选择器，服务端会话验证成员关系后才写入可信请求上下文；存储层再次验证对象空间归属，覆盖项目、任务、媒体及字幕/成片下载。撤销成员后续请求立即拒绝；已授权执行中的请求仍可能完成。

## 可替换OIDC集成

明确设置 `OIDC_ISSUER`、`OIDC_CLIENT_ID`、`OIDC_REDIRECT_URL` 才启用，机密客户端可另传 `OIDC_CLIENT_SECRET`。默认没有这些配置，不执行发现或外部网络调用。配置来自服务端，前端不能任意选择issuer。外部账户、客户端注册及凭据配置仍待用户另行授权。

`POST /api/v1/auth/sso/start` 返回authorization_url并设置5分钟HttpOnly浏览器绑定cookie。`GET /api/v1/auth/sso/callback` 接收授权码和state，验证绑定、一次性state、PKCE S256、nonce后，通过coreos/go-oidc验签并检查issuer/audience/expiry；另检查非空subject、email_verified和azp。仅接受RS256/ES256，配置与发现端点要求HTTPS；纯HTTP假issuer仅用于内部测试。固定回跳FRONTEND_ORIGIN，不接收客户端next/redirect目标。

身份以 `(issuer, subject)` 映射持久账号。新的SSO身份遇到相同本地邮箱拒绝，绝不自动合并账号或获取原组织权限。SSO账号不能用本地密码登录。既有subject经验证邮箱变更时先检查碰撞，再更新目录邮箱并撤销其旧sessions；不把旧邮箱永久保留为成员匹配入口，不自动合并其他账号。SSO错误返回通用信息，不回显token/凭据。未配置时 `sso_available=false`。state仅存单进程内存，重启需重新发起SSO；没有多实例会话或SSO单点退出。替换企业OIDC提供方需服务端显式配置；SAML未支持。

依据：[coreos/go-oidc官方仓库](https://github.com/coreos/go-oidc)、[官方Go包文档](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)。本地假issuer验证签名、nonce、audience、issuer、过期、空subject、未验证邮箱、azp、浏览器绑定、state过期和重放、PKCE、无自动邮箱关联。真实企业SSO未配置或验证。

## 数据迁移与恢复

`DATA_DIR/auth.json` v1单独保存账户、组织与session，权限0600，临时文件写入/fsync/rename/目录fsync。加载拒绝损坏和缺字段，不重置账户。它依赖FileStore整个目录的单进程锁，备份必须包含完整DATA_DIR。既有创作文件保持兼容；旧demo数据仅在显式demo模式查看，没有自动迁移归属。回滚旧二进制会移除身份安全边界，只能停服恢复匹配备份并回环运行。

这是单实例身份MVP。数据库、多实例、分布式限流、账号恢复和真实SSO验收仍不应由上述本地测试推断为生产就绪。
