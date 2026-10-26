# 真实小样的安全配置前置项

2026-10-23 只检查运行进程中以下变量是否非空，全部未注入；未读取文件、已有 secret 值或外部账号。USD3与CNY5分别批准，但没有认证传输确认，因此本轮不执行live。

| 厂商 | 凭据变量（仅名称） |
| --- | --- |
| OpenAI | OPENAI_API_KEY |
| Anthropic | ANTHROPIC_API_KEY |
| Google | GEMINI_API_KEY |
| Ark | ARK_API_KEY |
| MiniMax | MINIMAX_API_KEY |
| 独立豆包语音 | BYTEDANCE_SPEECH_API_KEY |
| 即梦 | JIMENG_ACCESS_KEY、JIMENG_SECRET_KEY |

模型及协议还需显式指定对应TEXT_MODEL/IMAGE_MODEL/VIDEO_MODEL/SPEECH_MODEL/ASR_MODEL或JIMENG_REQ_KEY；候选清单不是全部适配成功的证明。不得自动充值、兑换或将未知费用当零。每次实际敏感认证发送须满足父任务确认要求。

最小配置路径：

1. 使用执行平台提供的secret注入界面，在新启动的隔离后端／测试进程中注入必要变量，不放聊天、命令行参数、前端变量、Git或日志。若平台不能修改运行任务的secret环境，需要启动带注入的新环境；本任务不能替用户创建真实凭据。
2. 先运行默认dry-run，核对供应商、型号、规格和费用预留；USD 300分（3美元）与 CNY 500分（5元）独立上限。价格或最大输出不确定的case不执行，创建结果未知不重试。
3. 第八轮已接通托管模型执行的预算预留和输入／产出审核。全局环境变量选择的远端 provider 仍被拒绝，不能修改环境绕过治理。真实执行须显式选择租户模型配置及版本，具备有效报价、原币种预算和实际审核策略；配置存在不代表账号权限或模型契约已验证。
4. 模型管理页面可保存脱敏元数据或允许的secret引用；引用不会自动读取同名环境变量，需部署方显式绑定 tenant/config/provider 的 ApprovedSecretResolver；写入真实密文凭据还需另行授权。部署方从secret manager注入独立MODEL_CONFIG_KEK_BASE64，32随机字节，无默认值；本轮仅合成密钥单测，不生成用户持久凭据。KEK不可写入配置历史或备份包，丢失后不能解密原凭据。

OIDC、S3以及正式服务器、域名和付费服务另属部署授权。当前没有配置这些真实外部账号或资源。
