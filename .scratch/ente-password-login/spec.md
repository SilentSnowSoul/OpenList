# Spec: ente driver 密码登录

## 背景与动机

当前 ente driver（`drivers/ente/`）只支持 APIPages 凭证方式：管理员在配置里直接填
长期 account token 和 base64 master key。master key 是不可轮换的根密钥，明文存在
OpenList 数据库中，泄漏后无法通过改密码止损（Ente 改密码只重新包裹 master key）。

目标：新增账户密码登录方式，让泄漏面从"不可撤销的密钥本体"降为"可撤销的密码凭据"。

## 术语

- **APIPages 凭证模式**：管理员从 OpenList-APIPages 登录工具复制 token + master_key
  （+ 可选 secret_key）填入配置，driver 不做任何网络登录。现状行为。
- **密码模式**：配置里填 email + password，driver 在 Init 时自行完成 Ente 登录。
- **master key**：Ente 根加密密钥（DEK），不可轮换；泄漏 = 永久泄漏。
- **KEK**：argon2id(password, kekSalt) 派生的密钥加密密钥，只存在于密码持有者手中。
- **SRP-6a**：Ente 登录使用的 Secure Remote Password 协议（4096 位组）。

## 需求

### R1 配置项

- `meta.go` Addition 新增：`email`、`password`、`two_fa_secret`（可选，TOTP secret，
  存在时自动生成验证码；与 mega driver 的 `two_fa_secret` 语义一致）。
- `token`、`master_key` 由必填改为可选。
- help 文案说明两种方式：填 email/password（密码模式）或 token/master_key
  （APIPages 凭证模式）。

### R2 Init 优先级链（cloudreve_v4 模式）

1. `email` 与 `password` 均非空 → 密码模式：
   a. 若配置里已有 token：发送 `GET /users/session-validity/v2`，通过 `X-Auth-Token` 请求头携带 token；响应 JSON 包含 `hasSetKeys` 与 `keyAttributes`。只有 `hasSetKeys` 为真且 `keyAttributes` 可用时，才用 argon2id 派生 KEK 并在本地解密 master key / secret key。若没有密钥、缺少 `keyAttributes`、未授权（如 401）或请求失败，则放弃 token 快路径并进入 b 完整 SRP 登录；不请求 `GET /users/attributes`。

   b. 完整 SRP 登录（参照 OpenList-APIPages `frontend/src/lib/ente/` 的链路）：
      `srp/attributes` → argon2id KEK → blake2b loginKey → SRP create/verify-session
      → TOTP 自动填充（two_fa_secret 存在时）→ verify-session 返回 encryptedToken，
      sealed box 解出 account token → 写回 `token` 字段（`op.MustSaveDriverStorage`）。
   c. master key / secret key 只存内存，MUST NOT 写回配置或数据库。
2. 否则若 `token` + `master_key` 均非空 → APIPages 凭证模式，行为与现状完全一致
   （零迁移，已配置的存储不受影响）。
3. 都不满足 → Init 报错 `no way to authenticate: fill email/password or token/master_key`。

### R3 2FA 边界

- 服务端返回 two-factor 要求且 `two_fa_secret` 已配置：自动生成 TOTP 提交。
- 未配置 secret：报错提示需填 `two_fa_secret`。
- 邮箱 OTP（email verification code）：不支持，报错提示改用 APIPages 凭证模式。

### R4 错误呈现

密码登录各失败分支映射为管理员可读错误：密码错误、2FA 缺失/错误、邮箱 OTP 不支持、
网络错误，不透出裸错误码。

### R5 依赖与实现约束

- argon2id / blake2b 用 `golang.org/x/crypto`；SRP-6a（4096 组）与 sealed box
  （curve25519 + XSalsa20-Poly1305）优先用现有依赖，无则最小手写，不引重依赖。
- 登录链路的 Go 实现放 `drivers/ente/` 包内（如 `auth.go`），逐段对应 APIPages
  的 `client.ts` / `crypto.ts` / `login.ts`。

## 非目标

- Worker（OpenList-Worker）侧的登录实现——见该仓库同名 change。
- 邮箱 OTP 登录。
- master key / secret key 的任何形式持久化。
- 修改 APIPages 凭证模式的行为。

## 验收

- 密码模式：填 email+password（+2FA secret 如启用）后存储可用，List/读文件正常，
  数据库中只新增 token，不含 master key / secret key 明文。
- 凭证模式：现有 token+master_key 配置不做任何改动即可继续使用。
- token 失效场景：手动吊销 token 后 reload，自动走 SRP 重登并更新 token。
- 密码错误 / 2FA 缺失 / 邮箱 OTP 各分支返回可读中文或英文错误信息。
