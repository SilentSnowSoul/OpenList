# 03 — 2FA 分支与错误呈现

Status: resolved
Type: task
Blocked by: 02

## 目标

- two-factor 要求 + two_fa_secret 存在：自动 TOTP 提交（verify-session twoFactor 分支）。
- 未配置 secret：可读报错"fill two_fa_secret"。
- 邮箱 OTP（email verification code）：可读报错"not supported, use APIPages credentials"。
- 密码错误 / 网络错误映射为可读信息，不透出裸错误码。

## 验收

- 各失败分支错误信息明确、可区分；密码错误的用例有回归测试。

## Answer

- `auth.go`：verify-session 返回 twoFactorSessionID（V1/V2 兼容）时若有 `two_fa_secret` 自动 `totp.GenerateCode` 提交 `/users/two-factor/verify`；srpM2 在 2FA 之前校验（2FA 响应不带 srpM2）。
- 未配置 secret → `[Ente] 账号已开启两步验证,请在配置中填写 two_fa_secret`；SRP-less/邮箱 OTP → `[Ente] 该账号不支持密码登录...请改用 APIPages 凭证模式`；passkey → 同样指向凭证模式；401/404/429 经 `mapLoginError` 映射为中文可读信息，不透出裸状态码。
- 回归：`auth_test.go` 覆盖 2FA 成功/无 secret/错误 secret、邮箱 OTP 账号、passkey、密码错误（401 文案断言）各分支。
