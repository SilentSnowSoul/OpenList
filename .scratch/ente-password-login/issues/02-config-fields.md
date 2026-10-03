# 02 — meta.go 配置项与优先级链

Status: resolved
Type: task
Blocked by: 01

## 目标

`drivers/ente/meta.go` + `driver.go`：

- Addition 新增 `email` / `password` / `two_fa_secret`（two_fa_secret 自动 TOTP，
  参照 `drivers/mega/driver.go:40-46` 的 totp 用法）；`token` / `master_key` 改非必填。
- `Init` 按 cloudreve_v4 优先级链（`drivers/cloudreve_v4/driver.go:42-62`）：
  1. email+password → 若有 token，以 `GET /users/session-validity/v2` 携带 `X-Auth-Token: <token>` 请求；响应 JSON 包含 `hasSetKeys` 和 `keyAttributes`。仅在 `hasSetKeys` 为真且 `keyAttributes` 可用时使用该快路径进行本地解密；无密钥、缺少 `keyAttributes`、401/未授权或请求失败都必须回落到完整 SRP 登录，新 token 写回（`op.MustSaveDriverStorage`），master/secret key 只存内存。
     不请求 `GET /users/attributes`。
  2. 否则 token+master_key → 现行为不变。
  3. 全空 → `no way to authenticate` 报错。
- help 文案注明两种登录方式。

## 验收

- 密码模式 Init 后数据库 addition 中只新增 token，不含 master/secret key。
- 凭证模式存量配置零改动可用。

## Comments

## Answer

- `meta.go`：新增 `email`/`password`/`two_fa_secret`，`token`/`master_key` 改非必填，help 注明密码模式与 APIPages 凭证模式两种登录方式；`TestEnteAdminForm` 契约同步更新。
- `driver.go` `Init` 优先级链：email+password → token 快路径 + 完整 SRP 登录（新 token 经 `op.MustSaveDriverStorage` 写回，master/secret key 仅存内存）→ token+master_key 原行为 → 全空报 `no way to authenticate`。
- 偏差：museum 无 `GET /users/attributes`（只有 PUT，见 ente/server/cmd/museum/main.go:707），token 快路径改用 `GET /users/session-validity/v2`：请求头携带 `X-Auth-Token: <token>`，响应 JSON 包含 `hasSetKeys` 与 `keyAttributes`。仅在 `hasSetKeys` 为真且 `keyAttributes` 可用时走本地解密；无密钥/`keyAttributes`、401/未授权或请求失败均回落完整 SRP。
- 回归：`auth_test.go` 内存 SQLite 驱动 `MustSaveDriverStorage` 写回断言 token 落库、凭证模式测试零改动通过。
