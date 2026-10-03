# 02: Link 直链解析

**What to build:** 用户对 IwaraZip 挂载内任意文件请求直链时，驱动经 `/file/download` 解析出带 download_token 的签名 URL 并返回，可被复制/直接下载。

**Blocked by:** 01: IwaraZip 驱动骨架 + 认证 + List

**Status:** resolved

- [x] Link 两步解析：file id → /file/download → 签名 URL
- [x] 签名 URL 不缓存
- [x] 单元测试覆盖两步解析与失败透传
