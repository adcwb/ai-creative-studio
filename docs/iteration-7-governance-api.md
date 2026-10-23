# 预算治理 API 与安全边界

此阶段将预算账本接入应用启动与真实鉴权路由，不代表各供应商工作流已接入预留/结算。`live_execution_enabled` 仍为 false，服务端仍拒绝启动已就绪的真实付费配置。不会因设置预算而解除该限制。

## 端点

- `GET /api/v1/governance?space_id=...&currency=USD&offset=0`：当前组织成员可读取单币种报告。省略币种为USD；返回 `currency, minor_units, limit_minor, exposure_minor, available_minor, entries, total_entries, next_offset, budget_changes, live_execution_enabled, fx_available`。记录按更新时间倒序、ID稳定排序，每页100条，下一页偏移为null时结束。配置审计只展示当前组织/币种最近100条。
- `PUT /api/v1/governance/budget?space_id=...`：仅组织owner，要求已登录、可信Origin及CSRF token。body为 `{currency, limit_minor, expected_limit_minor}`；金额必须是0至9007199254740991的整数最小币单位。旧limit不匹配返回409；低于当前已知/未知费用占用返回400。成功返回报告。相同旧值/新值是无操作；网络结果不明时读取报告确认，不自动盲目覆写。
- `GET /api/v1/governance/exchange?space_id=...&from=USD&to=CNY`：会员只读报价。未配置可信汇率适配返回503 `exchange_unavailable`，金额保留原币种。同币种返回数学恒等比率1、来源`identity`，不是市场报价。真实报价接口保留来源、时间与陈旧标记，绝不用于静默修改原账本。

显式demo模式对上述所有端点返回403，不将演示space_id作为财务授权。viewer、editor、admin均不能提高或修改预算；移除组织成员后读取立即拒绝。客户端不存在费用预留、结算、退款或未知状态释放端点，防止伪造供应商账单。

## 账本与迁移

预算文件 `DATA_DIR/budget.json` 在FileStore持有排他目录锁后仅打开一次；每次请求不会重新读取/重置任务。缺少文件表示预算未配置，不能视为无限预算。每个币种独立计算：reserved和unknown保留完整预留，settled使用实际账单额，released仅在可信账单证据确认后释放。重启将未完成reserved转为unknown。

预算配置与操作者ID、组织、币种、前后值、UTC时间写入同一原子快照。该记录是本地操作审计，不是不可篡改审计仓库；总上限10000条，达到上限后拒绝新配置，不静默删除历史。新增可选changes字段兼容原v1账本，损坏审计字段拒绝启动。停服备份必须包含完整DATA_DIR；旧二进制写入可能丢失新增审计字段，回退须恢复匹配备份。

## 验证

`TestGovernanceAuthenticatedCurrencyBudget` 经真实SecureHandler覆盖未登录/陌生租户/CSRF/owner角色、所有非owner角色拒绝、跨币种隔离、未知费用预算保护、CAS与金额校验、demo拒绝、缺失汇率与不存在结算端点。`TestGovernanceBudgetAuditRecoveryAndPagination` 覆盖三位小数币种、分页、恢复后的未知状态与审计保留。最终全量集成与界面验收由统一验证记录汇总。
