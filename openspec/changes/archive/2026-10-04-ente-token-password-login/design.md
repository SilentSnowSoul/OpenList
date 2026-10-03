## Context

`Init`（drivers/ente/driver.go:49）当前按 `email && password` → `token && master_key` 两个分支判定；`initPasswordMode` 内的 `tryTokenFastPath` 已实现 token + password 解密路径，但被 email 门槛挡住。密码派生逻辑（deriveKEK / openKeyAttributes）位于 auth.go，无需改动。

## Goals / Non-Goals

- Goals: `token + password`（无 email）可完成初始化；token 失效时给出可读错误。
- Non-Goals: 不支持无 token 的 `password`-only 初始化（SRP 需 email）；不改动密文与协议层。

## Decisions

- `Init` 分支改为：`password != "" && (email != "" || token != "")` 走密码模式，保持 `token && master_key` 兜底不变；`password != ""` 但既无 email 也无 token 时返回明确错误。
- `initPasswordMode` 中 token fast path 失败后的 SRP 回退增加 email 判空：无 email 时直接返回「token 失效，请刷新 token 或补充 email」错误。
- 复用 `tryTokenFastPath` 原样，不抽新函数。

## Risks / Trade-offs

- 分支放宽后，`token + master_key + password` 组合的优先级从凭证模式变为密码模式。fast path 语义等价（同一 token 同一账号），且与 email + password + master_key 现有行为一致（密码模式优先并清空持久化密钥），风险低。

## Migration Plan

无迁移；存量 `token + master_key` 且未填 password 的配置不受影响。

## Open Questions

（无）
