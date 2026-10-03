# 01 — Ente 登录密码学原语（SRP-6a / sealed box）

Status: resolved
Type: task

## 目标

`drivers/ente/` 新增 `auth.go`（或 `srp.go` + `crypto_login.go`），移植
OpenList-APIPages `frontend/src/lib/ente/crypto.ts` 的登录链路为 Go：

- argon2id KEK（参数取自 `srp/attributes` 返回的 kekSalt/mem 属性）— `x/crypto/argon2`
- blake2b loginKey — `x/crypto/blake2b`
- SRP-6a 4096 组 create/verify session（手写，~200 行，对应 crypto.ts 的 SRP 段）
- sealed box 解 sealedbox（curve25519 + XSalsa20-Poly1305）开 encryptedToken
- keyAttributes 解密：KEK 开 `kekEncryptedMasterKey` 得 master key，派生 secret key

## 验收

- 单元测试：用 APIPages 已知的测试向量或固定 salt/password 断言派生结果；
  SRP round-trip（本地 mock verifier）通过。
- 不引重依赖（golang.org/x/crypto 除外）。

## Answer

- `drivers/ente/srp.go`：argon2id KEK、手写参数化 BLAKE2b（salt/personal，x/crypto 未暴露该参数化）、SRP-6a 4096 组完整实现；`openToken` 复用 `sealedBoxOpen`，`openKeyAttributes` 用 KEK 开 masterKey 后派生 secretKey。
- `drivers/ente/srp_test.go`：TS 参考实现交叉生成的 golden 向量（KEK/loginKey/srpA/M1/M2）+ SRP round-trip + 与 x/crypto 的等价性测试，全部通过。
- 无新增依赖。
- 实现要点：BLAKE2 每轮消息字配对为 m[s[i]],m[s[i+4]] / m[s[i+8]],m[s[i+12]]；末块零填充（无 0x80/长度标记）；恒保留一个 pending block 由最终 compress 收尾（key-only 时 key 块即末块）；final flag 置反 v[14]。
