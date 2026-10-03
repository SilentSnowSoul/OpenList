# 01: Ente crypto 模块与测试向量

**What to build:** Ente E2EE 解密原语的 Go 实现,可独立于任何驱动验证:secretbox 开箱(collectionKey、fileKey、metadata 各层级)、Curve25519 sealed box 开箱(共享相册 collectionKey)、XChaCha20 自定义 secretstream 的单块与流式解密、bs58 解码(Share URL fragment)。密文/密钥/nonce 均 base64 编码,时间戳为微秒。零新增 go.mod 依赖,仅用既有 `golang.org/x/crypto` 与标准库(math/big)。

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [x] secretbox open 与 sealed box open 通过固定测试向量(ente 官方向量 + OpenList-Worker `crypto.test.ts` 内已交叉验证的 GO_VECTOR,生成侧即 Go,直接搬运)
- [x] secretstream 多块流式解密、单块(metadata,TAG_FINAL)解密通过固定向量;乱序块与篡改密文必须拒绝;nonce 前进规则与 ente CLI 官方 Go 实现一致
- [x] sealed box nonce 语义按 Go 实现(blake2b-24(epk‖recipientPK)),非 libsodium sha256
- [x] bs58 解码与 worker collectionKey fixture 一致,长度校验 32 字节
- [x] 单测零外部依赖,随 `go test ./drivers/...` 全平台可跑
