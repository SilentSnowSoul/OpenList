# 01: IwaraZip 驱动骨架 + 认证 + List

**What to build:** 用户填入 endpoint/key1/key2（可选 root_folder_id）挂载 IwaraZip Storage 后，能看到 iwara.zip 账号根目录（或指定子目录）的文件夹与文件列表，子目录可逐层浏览。token 自动获取并在过期/失效时自动重取，API 错误（含 HTTP 200 + _status=error）以原始信息报给用户。

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] 驱动以 `IwaraZip` 注册并出现在挂载驱动列表，Addition 表单含 endpoint（默认 https://www.iwara.zip）/key1/key2（必填）/root_folder_id
- [x] key1+key2 换取 access_token + account_id，内存缓存，401 或 _status=error 的 token 失效自动重取且仅重取一次
- [x] List 返回 folders[] 与 files[] 的完整映射（名称/大小/时间/是否目录），无分页参数
- [x] _status=error 时错误信息透传；429 不重试
- [x] 单元测试覆盖：token 重取、错误透传、对象映射（mock HTTP 层）
