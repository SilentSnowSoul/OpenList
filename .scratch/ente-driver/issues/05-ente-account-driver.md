# 05: Ente 账号驱动

**What to build:** 以长期凭证(token + master_key,可选 secret_key)创建账号级只读挂载,`ente` 注册:根目录每个 collection(相册)一文件夹,进入后列出解密文件;仅两级目录结构。collection 列表走 `/collections/v2?sinceTime=0`,文件走 `/collections/v2/diff?collectionID=&sinceTime=`(复用 02 的分页水位);自有相册 key 用 secretbox、共享相册用 sealed box(`keyDecryptionNonce` 空值即共享,缺 secret_key 时跳过该相册并告警);隐藏相册按 magic metadata `visibility == 2` 判定,默认排除、`show_hidden` 展示;collection 名解密(空回退 `Ente-<id>`,`/` 替换为 `_`);请求头 `X-Auth-Token`;下载与缩略图复用 03/04 共享管线。

**Blocked by:** 04

**Status:** ready-for-agent

- [x] httptest mock 下根目录每个 collection 一文件夹,进入列出解密后的文件;请求序列仅 collections/v2 + per-collection diff
- [x] isDeleted collection 排除;隐藏相册默认不出现、show_hidden 开启后出现
- [x] 共享相册:提供 secret_key 时可列出,缺失时跳过并输出告警日志
- [x] 管理表单含 endpoint/token/master_key/secret_key/show_hidden;写操作全部 not supported
- [x] 账号驱动下载与缩略图复用共享管线(mock 端到端各一例)
