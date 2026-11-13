# CC5：自定义团队权限角色及所有权交接

状态：本地实现与验收；接续 `ab8cf94`，未推送/部署。个人作品迁移、团队解散和发布元数据余项另行推进，不把本阶段称作整个第6–10项完成。

## 用户流程与验收边界

所有者在“组织与成员”定义团队角色，选择创作、团队管理或只读的权限范围，并勾选实际授予的权限。范围创建后固定；权限可以减少或调整。角色不是仅改显示名称的固定角色别名：成员取得的是内建安全范围与当前自定义权限的交集，API和独立worker均读取实际权限。所有者专属的管理员授予/角色定义/所有权交接不在自定义角色可授予集合中。

创建角色使用客户端保留的稳定随机ID；更新同时核对团队和角色版本。已存在且内容相同的v1创建、与预期v+1完全一致的更新可以只读核对，不重复写入。后来的不同修改会冲突，不能沿旧稿覆盖。删除仍分配给成员的角色会被拒绝；不存在的角色删除作为只读幂等完成。界面保留原请求恢复和明确确认，不用超时后换ID掩盖结果未知。

成员分配必须使用可信渠道核对的账号ID和邮箱，新增 `custom_role_id` 只接受当前团队定义。旧客户端继续可用固定角色；自定义分配必须提供当前团队版本。任何角色的实际授权不能超过操作者当前拥有的权限。不能修改所有者身份、跨团队引用定义、通过基础角色上限隐式获得没有勾选的能力。

在“团队空间”中，当前所有者可以选择现有成员，核对完整邮箱及账号ID、明确确认后交接。团队资料、作品和空间ID保持原处，新所有者取得所有者权限且清除其原自定义分配，原所有者成为管理员。所有者行、成员行和团队版本同事务改变，不出现无所有者中间状态。此确认是操作者的明确动作，**不冒充目标方已收到可信邮件或已完成外部同意认证**。网络结果未知时重新读取当前所有者，不盲目重复移交。

不增加订阅、费用或项目额度，不恢复审核前置，不发送真实模型/邮件请求。

## 领域与存储

`identity.AuthOrganization` 增加可选：

- `custom_roles[id] = {id,name,base_role,version,permissions[]}`，内建范围仅 admin/editor/viewer。
- `member_roles[user_id] = role_id`，只有当前非所有者成员可分配；基础 `members[user]` 用于新版本的兼容上限，不能作为实际权限。
- `identity.EffectivePermissions/EffectiveAllows` 统一求交，规范化去拒重复/未知/越范围权限，保留 workspace.read；模型执行及管理动作要求 workspace.write，成员管理还要求 members.read。

PostgreSQL增加受约束关系表：

- `studio_space_roles(space_id,id,name,base_role,version)`，联合主键、范围约束及只接受team空间的复合外键。
- `studio_space_role_permissions(space_id,role_id,permission_id)`，定义外键及全局已知权限外键。
- `studio_space_member_roles(space_id,user_id,role_id,base_role,membership_role)`，分配同时受成员身份和同空间角色/范围的联合外键约束。
- 原成员表增加 `(space_id,user_id,role_id)` 唯一身份约束，用于关联分配。

关系表是PG当前授权权威；组织JSON是同事务的导出表示。初始化不会从旧JSON擅自恢复操作员已经撤销的关系权限。无效关系、缺失策略、DB失败不会回退至内建范围或旧缓存。角色更新和分配都复用现有AuthStore事务锁、版本及审计；有效权限变化会取消同实例活动模型请求，跨实例由既有实时授权监测/派发前检查生效。

### 防止旧版本忽略新角色后越权

- 启用自定义角色后认证文件升为格式2，删除最后一个角色也不自动降回格式1。新版本读取1/2并验证策略；旧文件读取器拒绝格式2。
- PG中的自定义成员原生 `role_id='custom'`，该保留标识没有内建授权。新读取器必须读取受约束的同空间分配/定义才解析出兼容上限。旧读取器不认识该角色，会拒绝授权，不能把自定义只读成员当成内建editor/admin放行。
- 自定义成员未配齐策略时拒绝，不能静默降级为更宽的内建角色；旧二进制混跑因此可能不可用，这是安全边界，不能声明混合版本兼容。
- 升级须先备份匹配数据库和DATA_DIR、停止所有writer、运行新initializer，再用匹配的API/worker/前端恢复。不是线上自动迁移或可用性保证。回退必须恢复整套匹配备份，不能仅切回旧镜像/删掉格式号或策略字段。

## 接口与前端

```
GET/POST/PUT/DELETE /api/v1/organizations/{id}/roles
PUT /api/v1/organizations/{id}/members  # 可选custom_role_id
POST /api/v1/organizations/{id}/transfer
GET /api/v1/organizations/{id}/access  # 有效permissions及可选custom_role_id/name
```

成员/角色清单仍要求members.read。自身access和主动退出只要求workspace.read，避免没有成员列表权限的人不能查看自己的权限或离开团队。个人空间仍不能扩为共享空间。

前端复用既有设计token、表单和恢复机制；图像/短剧、资料、库、剪辑、系列等写入按钮由 `hasPermission(...,'workspace.write')` 判断，取消原来仅看 `role!=='viewer'` 的假设。模型目录也使用当前models.execute；成员及使用统计导航按对应权限展示。旧演示ID只保留明确的本地测试兼容，显式空permissions始终拒绝，绝不据此授权服务器操作。

关键代码：`modules/identity/custom_roles.go`、`application/auth_{custom_rbac,roles_http,rbac,http,store,space_settings}.go`、`model_runtime.go`、`postgres{,_health}.go`；前端 `CustomRolesPanel.tsx`、`OrganizationPanel.tsx`、`TeamSpaceActions.tsx`、`permissions.ts` 及各写入入口；契约在 `api/openapi.json`。

## 验证与未验收项

- 文件/真实本地PG：角色创建/回放、范围越权拒绝、只读成员不能管理角色、CSRF、当前空间和团队CAS、成员分配、分配中禁止删除、更新权限即时生效、已有模型调用取消、另一API实例撤权、模型执行拒绝、原角色请求保护后续修改、所有权交接及重启持久化。
- PG→文件→另一个PG往返保留实际权限；验证PG保留角色不能被旧validRole读取器解释，文件格式2标记存在。旧二进制整进程未在本机运行，不能宣称测过旧版本全部启动行为。
- 既有RBAC旧数据/坏数据原子迁移夹具同时移除新关系表以真实模拟旧schema，不通过删掉断言来规避约束。
- 浏览器桌面1920×1080和iPhone13：角色创建响应丢失、页面重载原请求恢复、实际成员分配、编辑角色取消作品编辑和模型执行、成员UI隐藏写入且实际API403、人工确认交接、跨个人空间403、上游请求数0。
- 未运行真实生产数据库迁移、Windows原生、真实企业流程/邮件送达/付款或付费模型。

自定义角色允许的权限粒度以当前服务器实际支持的9类权限为边界；不伪造逐项目ACL或每个镜头的独立权限。组织JSON中的基础角色只是安全上限；客户端必须用有效permissions，不据名称推断权限。团队解散、个人作品迁移和商业版计费均未因这些角色表自动完成。

### 本地最终检查（2026-11-13）

- `go test ./... -count=1` 全文件模式通过，application 147.134秒。最终真实本地PG `go test -race ./internal/application ./internal/modules/identity -run 'CustomRole|RBAC|TeamSpace' -count=1 -timeout=120s` 通过，application 35.216秒，包含独立管理权限改名、无成员读取权限退出及PG/文件往返。之前更广的Auth/runtime race通过105.031秒，相关模型执行门禁10.684秒。
- 前端24文件79单测、类型、Lint、生产构建通过；OpenAPI验证及3018条国际化检查通过。Go vet、Windows amd64 server/worker交叉编译通过；交叉编译不等于Windows原生验收。
- `custom-roles` 与既有 `creator-workflow` 浏览器回归桌面7项、手机7项全部通过；最终角色闭环另复验桌面1项、手机1项通过，合成上游调用数为0。
- 1920×1080及iPhone13实际截图已检查，证据保存在工作环境 `/workspace/artifacts/creator-completion/{custom-roles,custom-role-editor,team-transfer}-{desktop,mobile}.png`。截图不是生产环境或真实许可的证明。
- 此阶段仅本地提交；尚未取得这些新提交的云端CI结果。CC6迁移与CC7剩余交付/关闭流程继续实施，不提前推送。
