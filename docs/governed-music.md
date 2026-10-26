# 受治理的器乐生成

本实现仅将已有 MiniMax 器乐适配器接入租户运行时和既有创作 worker，不创建账号、探测资格或执行真实请求。2026-10-26只读核对[官方 Music Generation](https://platform.minimax.io/docs/api-reference/music-generation)及[价格说明](https://platform.minimax.io/docs/pricing/overview)：付费音乐API限已有付费用户，新用户不再开放；因此默认仍拒绝音乐运行时，不能把模型ID可配置当作服务资格。

## 操作员资格与运行时边界

部署操作员在 `EXECUTION_POLICY_FILE` 中增加 `music_entitlements`，每条必须绑定 `tenant_id`、`model_config_id`、该租户配置的完整 `model_config_version`、`model`、`contract`、`source`和未来`expires_at`。合同只允许 `minimax-existing-paid-account-v1`，模型只允许已实现的 `music-3.0`／`music-2.6`。来源是操作员实际核验的依据，不是固定示例确认。重复配置、过期、未知合同或不完整绑定拒绝加载。此文件由平台操作者维护，不在租户管理API可写字段内。

资格是启动加载的操作员声明，不是自动查询供应商得到的证明。修改文件本身不会热重载；更新资格策略需受控重启相应API/worker。要立即停止后续执行，使用已有租户模型停用/凭据撤销或成员撤权；配置变更使旧版本资格失效，重新启用必须再次核准该新版本。到期在每次发送前复核，在途请求也由已有授权监视器保守取消，但取消不等于供应商停止计费。

所有运行时入口同时检查当前成员角色、租户/配置版本、能力、启停/生命周期、资格和费用permit。只有通过这些检查才解密租户凭据。缺资格时目录模型`available=false`，报价和任务接受均拒绝；即使绕过UI也不能调用。全局 `MUSIC_PROVIDER` 环境路径继续受启动治理护栏约束，不作为托管路径的fallback。

## 请求、费用与产出

前端沿用“器乐音乐”与组织模型选择，明确显示存量付费账户限制。用户获得当前请求哈希报价并确认原币种费用上限后提交；修改prompt、素材或配置版本会使旧确认失效。音乐没有通用默认价格，必须匹配可信精确请求规则；此资格声明不代替报价、审核或预算。

实际固定协议：`POST https://api.minimax.io/v1/music_generation`，显式模型、`is_instrumental=true`、`stream=false`、`output_format=hex`，44100Hz、128000bit/s MP3。本轮不支持歌词、翻唱、免费/退役型号或其他音乐厂商协议；不会静默更换型号或退回模拟。单响应与解码产物均受容量上限，入库再经实际媒体校验。

音乐走通用治理链：输入审核、原币种预算、发送前持久unknown、可信账单证据结算；没有账单证据则保留unknown，即使HTTP成功或媒体生成失败。只在运行时证明从未尝试网络发送时释放预留。无自动重发或重复收费提交。音频产出须审核通过，再由媒体存储创建带幂等回执的music素材；失败不得发布未经审核的音频。已有PostgreSQL creative worker使用同一执行函数，无新增进程内付费旁路。

## 本地验证与外部边界

测试使用合成账号/密钥、拦截HTTP transport及FFmpeg正弦波MP3，覆盖资格绑定/过期、跨租户/能力拒绝、精确原币种报价、输入/输出审核、发送前撤销释放、在途资格撤销保留unknown、不得重发、真实音频入库。HTTP报价/接受测试也包含音乐与请求变化拒绝。

没有验证真实账户资格、音乐质量、供应商计费、延迟或正式运维；也没有使用已批准的USD3/CNY5小样预算。资格核验、真实凭据配置和实际认证发送仍须由父任务处理授权，不通过本地fixture假装完成。

本阶段验证：`TestMusic*`、`TestPostgresMusic*`、`TestExactRequestQuoteAcross*`、原 `TestModelRuntime*` 与 `TestExecutionGovernance*` 的 targeted race通过（18.598秒，显式本地PostgreSQL DSN）。新增音乐HTTP无资格409负向用例单独race通过（2.798秒）；CreativeTools单文件eslint通过。完整产品回归由根任务在所有模块冻结后统一运行，本阶段不代替全量验收。
