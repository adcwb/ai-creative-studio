# 离线模型凭据 KEK 轮换

`backend/cmd/model-key-rotate`对当前文件或PostgreSQL配置中已有的AES-256-GCM密文重新加密。它不创建供应商凭据，不联系供应商，不改外部权限，也不从旧备份或历史恢复已撤销的secret。

## 操作边界

先停止**全部**API、worker和可能自动重启它们的控制器，备份完整数据并单独保管旧KEK。文件模式要求已有`state.json`和0600普通`model-config.json`，排他占有`store.lock`，拒绝符号链接、未知JSON字段和过大数据。PostgreSQL要求完整已初始化schema，获取非阻塞维护排他锁及模型scope行锁；锁不能代替停止队列worker。

操作者通过安全的进程secret注入三项base64值（均解码为32字节）：

- `MODEL_ROTATION_OLD_KEK_BASE64`：当前KEK。
- `MODEL_ROTATION_NEW_KEK_BASE64`：另行安全生成的新KEK，必须不同于旧值。
- `MODEL_ROTATION_CONFIRM_KEK_BASE64`：再次明确提供相同的新值，防止误传或截断。

不要在命令参数、聊天、Git或报告放密钥。本工具不自动生成或持久化密钥，不读取`MODEL_CONFIG_KEK_BASE64`作为隐式回退。新KEK仍需操作者分别安全提供给所有恢复运行的API/worker。

```sh
# 默认file；以上三项密钥已经由安全注入提供，命令参数不含密钥。
./model-key-rotate --data-dir=/private/stopped/data --confirm-stopped
./model-key-rotate --data-dir=/private/stopped/data --confirm-stopped --apply
# PostgreSQL：另注入STORAGE_BACKEND=postgres、明确DATABASE_URL URI。
./model-key-rotate --confirm-stopped
./model-key-rotate --confirm-stopped --apply
```

默认只验证及预览。`--apply`才写入。PostgreSQL连接须明确host/user/database/sslmode，不接受service/passfile/profile引用，不读取操作者默认.pgpass；内部使用独立空密码文件。数据库错误和密钥不回显。

## 原子性、版本和回退

每个当前加密条目先用旧KEK及原tenant/config/provider AAD验证，再用随机nonce和新KEK重封装；所有条目验证成功后才能提交。每个发生变化的租户版本递增一次，旧版本任务不能继续使用原配置许可。绑定配置版本的操作员音乐资格也随之失效，须另行重新审核并配置；轮换命令不会自动保留或重发该资格。引用状态保持引用，撤销条目保持禁用且无密文，未配置条目保持未配置。不存在活动加密条目时是无写入操作。

文件采用临时文件、fsync、rename及目录同步；验证/写入前失败不会产生部分轮换。PG中全部密文、版本和脱敏`MODEL_KEY_ROTATE`审计同一事务提交，审计失败也全部回滚。文件模式返回脱敏操作者报告，操作者应保存到受控审计位置；不自动改写其他审计档案。

报告仅含变更租户/条目数、是否应用及前后快照SHA256，没有密钥、密文或供应商secret。预览和实际执行分别产生随机nonce，因此预览的after摘要不是下一次apply的固定预期值；实际提交报告才记录实际新状态。

提交之后的连接中断、目录fsync失败等可能留下**结果不确定**状态；错误不会保证旧状态仍在。保持全部服务停止，分别验证状态和密钥后处理，不能自动重试或盲目恢复旧备份。明确完成的轮换可用新KEK作为old、原KEK作为new，再次对**当前**配置执行逆向轮换；版本继续递增，撤销记录不会恢复。历史备份仍需要当时匹配的KEK，不会被此命令改写。

## 合成验证

`go test -race ./internal/studio ./cmd/model-key-rotate -run 'TestModelRotation|TestPostgresModelRotation|TestRotationConnection' -count=1`

覆盖文件预览/应用/逆向、错误及相同密钥、确认不符、跨租户密文、撤销污染、版本溢出、锁/符号链接/权限/严格JSON拒绝；PG另测预览无审计、审计失败事务回滚、维护锁拒绝、成功审计与逆向状态。真实PG测试仅在显式`STUDIO_POSTGRES_TEST_DSN`提供隔离测试库时运行，每次创建并清理自己的随机schema；缺变量则skip，不得算作集成通过。仅使用合成密钥和secret，不做真实用户密钥轮换。

2026-10-26在Go1.25.3和独立PostgreSQL17.11合成数据库执行上述七个主测试及文件安全子测试，`-race`通过：studio 1.284s、CLI 1.050s。证据`/tmp/studio-model-rotation-race.log`，是本地专项验证，不替代父任务最后完整回归或真实用户KEK操作。
