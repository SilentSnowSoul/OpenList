## Purpose

ente 存储驱动支持用账户密码登录 Ente 账号，替代在配置中持久化不可轮换的 master key。

## ADDED Requirements

### Requirement: 密码模式配置项
ente 驱动 SHALL 接受 `email`、`password`、`two_fa_secret`（可选，TOTP secret）配置项；`token`、`master_key` SHALL 变为可选，且 help 文案 SHALL 说明两种登录方式。

#### Scenario: 仅填 email 与 password
- **WHEN** 管理员填写 email 与 password 并保存存储
- **THEN** 驱动以密码模式初始化

#### Scenario: 两套凭证均未填
- **WHEN** email、password、token、master_key 均为空
- **THEN** 初始化失败并提示需填写任一登录方式

### Requirement: 密码模式初始化优先级链
驱动初始化 SHALL 按优先级认证：密码模式下若已有 token，则用 token 获取 keyAttributes 并以 argon2id(password) 派生 KEK 本地解密 master key 与 secret key；token 失效时 SHALL 回退完整 SRP 登录并将新 token 写回配置；master key 与 secret key MUST NOT 被持久化。

#### Scenario: 持久化 token 有效
- **WHEN** 密码模式且配置中 token 有效
- **THEN** 跳过 SRP 握手，用 token + 密码派生 KEK 完成初始化

#### Scenario: token 失效
- **WHEN** token 被吊销或过期
- **THEN** 自动执行 SRP 登录，新 token 写回，密钥仅存内存

### Requirement: 凭证模式向后兼容
已存在的 token + master_key 配置 SHALL 继续工作，无需迁移。

#### Scenario: 存量存储不受影响
- **WHEN** 存储仅配置 token 与 master_key
- **THEN** 行为与变更前一致

### Requirement: 2FA 与错误呈现
启用 2FA 且配置 two_fa_secret 时 SHALL 自动生成 TOTP 验证码；邮箱 OTP 场景 SHALL 返回明确的不支持错误；密码错误、2FA 缺失、网络错误 SHALL 映射为管理员可读信息。

#### Scenario: 自动 TOTP
- **WHEN** 服务端要求 two-factor 且已配置 two_fa_secret
- **THEN** 驱动自动提交 TOTP 完成登录

#### Scenario: 邮箱 OTP 不支持
- **WHEN** 服务端要求邮箱验证码
- **THEN** 返回可读错误并指引使用 APIPages 凭证模式
