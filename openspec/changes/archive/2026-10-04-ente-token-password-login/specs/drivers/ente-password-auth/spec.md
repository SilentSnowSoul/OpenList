## MODIFIED Requirements

### Requirement: 密码模式配置项
ente 驱动 SHALL 接受 `email`、`password`、`two_fa_secret`（可选，TOTP secret）配置项；`token`、`master_key` SHALL 变为可选，且 help 文案 SHALL 说明可用的凭证组合：`email + password`、`token + password`、`token + master_key`。

#### Scenario: 仅填 token 与 password
- **WHEN** 管理员仅填写 token 与 password 并保存存储
- **THEN** 驱动以密码模式初始化，用 token 获取 keyAttributes 并以 argon2id(password) 派生 KEK 本地解出 master key 与 secret key

#### Scenario: 仅填 email 与 password
- **WHEN** 管理员填写 email 与 password 并保存存储
- **THEN** 驱动以密码模式初始化

#### Scenario: 两套凭证均未填
- **WHEN** email、password、token、master_key 均为空
- **THEN** 初始化失败并提示需填写任一登录方式

#### Scenario: 仅填 password
- **WHEN** 管理员仅填写 password，未填 email 与 token
- **THEN** 初始化失败并提示凭证不足

### Requirement: 密码模式初始化优先级链
驱动初始化 SHALL 按优先级认证：密码模式下若已有 token，则用 token 获取 keyAttributes 并以 argon2id(password) 派生 KEK 本地解密 master key 与 secret key；token 失效时，若配置了 email 则回退完整 SRP 登录并将新 token 写回配置，若未配置 email 则返回明确的可读错误；master key 与 secret key MUST NOT 被持久化。

#### Scenario: 持久化 token 有效
- **WHEN** 密码模式且配置中 token 有效
- **THEN** 跳过 SRP 握手，用 token + 密码派生 KEK 完成初始化

#### Scenario: token 失效
- **WHEN** token 被吊销或过期，且配置了 email
- **THEN** 自动执行 SRP 登录，新 token 写回，密钥仅存内存

#### Scenario: token 失效且无 email
- **WHEN** token 被吊销或过期，且未配置 email
- **THEN** 初始化失败，返回提示刷新 token 或补充 email 的可读错误
