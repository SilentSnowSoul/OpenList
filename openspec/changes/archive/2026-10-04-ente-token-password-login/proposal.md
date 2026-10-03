## Why

现状 `Init` 只接受两种凭证组合：`email + password` 与 `token + master_key`。但 Ente 还存在第三种合法组合 `token + password`（不填 email）：token 获取 keyAttributes 后，用 argon2id(password) 派生 KEK 即可本地解出 master/secret key。该路径已实现于密码模式的 fast path（`tryTokenFastPath`），却被 email 非空的门槛挡住，用户无法直接使用。

## What Changes

- 放宽密码模式的准入条件：`token + password`（无需 email）即可完成初始化，复用现有 fast path（fetch keyAttributes → deriveKEK → openKeyAttributes）。
- `token + password` 模式下 token 失效时无法回退 SRP 登录（缺少 email），返回明确的可读错误。
- 更新 `meta.go` help 文案，说明三种凭证组合。
- 其余行为（密钥不持久化、凭证模式兼容）保持不变。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `drivers/ente-password-auth`: 密码模式准入从「email + password 必填」放宽为「password 必填且（email 或 token 至少其一）」，并明确 `token + password`（无 email）模式的行为边界——仅走 fast path，token 失效报错而非 SRP 回退。

## Impact

- `drivers/ente/driver.go`：`Init` 分支条件与 `initPasswordMode` 的错误路径。
- `drivers/ente/meta.go`：help 文案。
- `drivers/ente/driver_test.go`：新增分支测试。
- 无 API/依赖变更。
