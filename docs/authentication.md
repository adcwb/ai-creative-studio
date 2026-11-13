# 身份、空间与OIDC

默认 `AUTH_MODE=secure`，所有创作、素材和下载接口要求登录；`/healthz`、`/api/v1/auth/session` 无需登录，登录注册与SSO入口有独立检查。`AUTH_MODE=demo` 明确保留无身份演示模式，不能作为多租户安全边界。新账号只拥有随机 `org-…` 工作空间，不能认领旧 `personal-demo` / `team-demo` 数据。

## 配置与边界

- `AUTH_ALLOW_REGISTRATION=true` 才开放自助注册（含首次SSO账号），默认关闭。不创建默认密码，不覆盖已有账号。操作者可明确开启注册，让用户自行注册后关闭；也可按下述停写流程离线引导新本地账号。
- `AUTH_COOKIE_SECURE` 默认true；仅HTTP回环开发显式设false。会话cookie为HttpOnly、SameSite=Lax、12小时；前端请求携带credentials。
- 安全模式只允许 `FRONTEND_ORIGIN` 和显式 `AUTH_ALLOWED_ORIGINS` 中的完整来源（scheme/host/port），不信任Host、转发头或Referer推导；任意、null、通配符来源拒绝。受信来源的写请求仍要求会话CSRF。旧跨域部署升级前须列出全部真实前端来源，不能继续任意来源携带凭据。只有显式demo模式保留开放CORS；签名媒体分享GET/HEAD及其仅Range预检单独允许无凭据的公开读取，不作为登录或管理入口。
- 登录/注册每来源连接IP每15分钟20次、最多1024个限流桶。默认不信任转发头。部署可明确设置 `AUTH_TRUSTED_PROXY_CIDRS`（逗号分隔CIDR，最多32项），仅当TCP对端在该列表才使用其覆盖写入的 `X-Real-IP`；不读取X-Forwarded-For，非法地址拒绝。Nginx必须用 `$remote_addr` 覆盖X-Real-IP，不能传递客户端原头。Docker配置仅信任专用内部网络中的前端代理单IP；额外公网代理链须独立审查。没有分布式限流。每用户最多20个未过期session。
- 密码为12–128字节，独立随机16字节盐，PBKDF2-HMAC-SHA256 600000次、32字节派生值；session只存SHA256摘要。原密码和原session token不落盘。
- 暂无本地账号邮件验证、密码重置、MFA、其他设备退出及owner转移。自助注册不证明邮箱所有权，因此绝不自动以同邮箱绑定SSO。

## API与权限

`GET /api/v1/auth/session` 返回mode、authenticated、registration_enabled、sso_available；已登录时另含user `{id,email}`、csrf_token、spaces。`POST /api/v1/auth/register`、`/login` 接受 `{email,password}` 并设置会话cookie；`POST /api/v1/auth/logout` 撤销当前session。

`GET /api/v1/spaces` 只列当前用户可访问的个人与团队空间。`POST /api/v1/organizations` 接受 `{name}` 创建团队空间并成为owner。`GET /api/v1/organizations/{id}/members` 要求 `members.read`，内置四角色均可读取本空间成员；`GET /api/v1/organizations/{id}/access` 返回当前角色、实际权限和成员版本。

`PUT /api/v1/organizations/{id}/members` 接受 `{email,user_id,role,expected_version}`，role为admin/editor/viewer/remove，仅团队owner/admin可写。所有目标账号（本地或SSO）都必须提供已注册的稳定 `user_id`，且与目录中的当前邮箱匹配；不发送邮件或邀请。管理员须通过可信渠道核对对方主动分享的ID，不能仅凭自填或可能复用的邮箱授权；已有成员UI自动附带服务器记录ID。admin不能修改owner/admin，owner不能被降级或删除，个人空间不可添加他人。不设置组织或成员数量配额。

新前端始终从access接口读取并发送 `expected_version`；版本不一致返回409。旧客户端可以省略版本，以串行最后写入生效，不保证CAS；只有邮箱而无 `user_id` 的旧请求会被拒绝，必须升级。

viewer只读，editor可修改创作内容并使用模型，admin/owner另能管理模型与成员。space_id只是选择器，服务端会话验证成员关系后才写入可信请求上下文；存储层再次验证对象空间归属，覆盖项目、任务、媒体及字幕/成片下载。撤权或降级在下一次授权复核/派发前生效，跨实例运行时轮询取消本地等待；已发送上游的工作不能保证撤回，结果按不确定处理，不自动重发。数据库读取失败拒绝授权，不使用缓存角色。

## 可替换OIDC集成

明确设置 `OIDC_ISSUER`、`OIDC_CLIENT_ID`、`OIDC_REDIRECT_URL` 才启用，机密客户端可另传 `OIDC_CLIENT_SECRET`。默认没有这些配置，不执行发现或外部网络调用。配置来自服务端，前端不能任意选择issuer。外部账户、客户端注册及凭据配置仍待用户另行授权。

`POST /api/v1/auth/sso/start` 返回authorization_url并设置5分钟HttpOnly浏览器绑定cookie。`GET /api/v1/auth/sso/callback` 接收授权码和state，验证绑定、一次性state、PKCE S256、nonce后，通过coreos/go-oidc验签并检查issuer/audience/expiry；另检查非空subject、email_verified和azp。仅接受RS256/ES256，配置与发现端点要求HTTPS；纯HTTP假issuer仅用于内部测试。固定回跳FRONTEND_ORIGIN，不接收客户端next/redirect目标。

身份以 `(issuer, subject)` 映射持久账号。新的SSO身份遇到相同本地邮箱拒绝，绝不自动合并账号或获取原组织权限。SSO账号不能用本地密码登录。既有subject经验证邮箱变更时先检查碰撞，再更新目录邮箱并撤销其旧sessions；不把旧邮箱永久保留为成员匹配入口，不自动合并其他账号。SSO错误返回通用信息，不回显token/凭据。未配置时 `sso_available=false`。OIDC state仅存发起流程的单进程内存，回调须回到该进程，重启需重新发起SSO；这与PostgreSQL模式下共享的应用会话不同。未实现SSO单点退出，替换企业OIDC提供方需服务端显式配置；SAML未支持。

依据：[coreos/go-oidc官方仓库](https://github.com/coreos/go-oidc)、[官方Go包文档](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)。本地假issuer验证签名、nonce、audience、issuer、过期、空subject、未验证邮箱、azp、浏览器绑定、state过期和重放、PKCE、无自动邮箱关联。真实企业SSO未配置或验证。

## 数据迁移与恢复

`DATA_DIR/auth.json` v1单独保存账户、组织与session，权限0600，临时文件写入/fsync/rename/目录fsync。加载拒绝损坏和缺字段，不重置账户。它依赖FileStore整个目录的单进程锁，备份必须包含完整DATA_DIR。既有创作文件保持兼容；旧demo数据仅在显式demo模式查看，没有自动迁移归属。回滚旧二进制会移除身份安全边界，只能停服恢复匹配备份并回环运行。

文件模式要求单实例writer。PostgreSQL模式已实现共享应用会话、实际RBAC关系表及跨API/worker授权复核，并以临时数据库验证多实例撤权与隔离；生产配置、分布式限流、账号恢复及真实企业SSO仍未由这些合成测试证明就绪。数据库迁移须备份完整数据库与DATA_DIR、停止全部旧writer，并按[P0规范](superpowers/specs/2026-11-06-p0-administration/README.md)使用同版本离线工具；不能新旧writer混跑或仅降级二进制回退。

## 部署管理员与离线引导

部署管理员只由 `STUDIO_ADMIN_USER_IDS` 中经操作者核对的不可变账号ID授权，不按邮箱授权，也不继承空间owner/admin权限。旧 `STUDIO_ADMIN_EMAILS` 仅保留为禁止公开抢注的保留名称，已有同名账号不会自动升级。已有受控账号可通过可信渠道核对ID；需新账号时，停写后使用 `/app/bootstrap-user -email <email> -confirm-stopped`，密码只从stdin输入，命令仅输出新 `user_id`，不覆盖同名账号、不自动授权。将ID加入部署环境后重启同版API/worker。

`-confirm-stopped` 只是操作者的停写确认，不会停止进程、发现或禁止PostgreSQL全实例写入。工具仅尝试锁定当前DATA_DIR的 `store.lock`，不能排除使用其他DATA_DIR的API、worker或其他数据库writer。操作者必须先停止所有实例、后台writer和自动重启控制器，并保持停写直到操作完成；PostgreSQL须先完成schema初始化。完整操作说明见[P0安全迁移](superpowers/specs/2026-11-06-p0-administration/README.md)。


## 个人/团队与RBAC（2026-11-06）

新账号拥有明确的个人空间，仅本人可访问，不允许邀请他人。团队空间在“空间与协作 → 团队空间”创建，成员页面展示角色说明与权限调整；旧组织保持team及原ID和成员，登录时独立补建personal，不搬移旧资源。owner/admin/editor/viewer的权限矩阵、真实关系表、CAS接口和停写迁移要求见[P0规范](superpowers/specs/2026-11-06-p0-administration/README.md)。空间所有者不等于部署管理员，不会获得全局后端配置权限。团队基础不包含席位或使用额度限制。


安全复审更新：部署管理员必须由`STUDIO_ADMIN_USER_IDS`固定不可变账号ID，邮箱名单不再授权。旧`STUDIO_ADMIN_EMAILS`只保留为禁止公开抢注的保留名称，已存在同名未验证账号不会自动升级。成员添加/变更一律需要明确`user_id`与邮箱匹配，本地邮箱未验证，需在可信渠道核对ID；SSO也不再接受仅邮箱授权。旧客户端只有邮箱的写请求将被拒绝，必须升级；无`expected_version`但带正确ID的旧请求仍按串行最后写入生效，不保证CAS，新前端始终发送版本。详见[P0安全迁移](superpowers/specs/2026-11-06-p0-administration/README.md)。


## 团队自定义角色与格式2升级

团队所有者可以在组织与成员页创建权限角色；基础范围只是安全上限，实际操作以当前有效permissions为准。可手动编辑作品的角色不自动获得模型执行权。角色分配受团队范围、成员账号ID和版本约束；旧客户端固定四角色仍可使用，自定义角色要求当前版本。当前所有者可在团队空间中明确交接给现有成员，原所有者成为管理员，团队作品不会移动。

启用自定义角色后，认证文件为格式2；PG中的自定义成员使用旧读取器不接受的保留标识，并依赖新增的同空间角色/权限/分配约束表。请停止所有API/worker writer、保留匹配数据库与DATA_DIR备份，按既有部署方式运行新版本初始化，并用匹配版本的API、worker和前端恢复服务。旧二进制不能安全解释新策略，不支持新旧版本混跑或只回退镜像；不要删除格式标记或权限字段来强行降级。回退必须恢复匹配的整套备份。

实现、实际验收及未完成边界见 [团队角色规格](superpowers/specs/2026-11-13-creator-completion/roles-implementation.md)。这不提供商业支付、可信邮件验证或逐项目ACL，也不把本地合成测试当成企业生产验收。
