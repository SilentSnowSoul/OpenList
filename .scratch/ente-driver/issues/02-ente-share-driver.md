# 02: EnteShare 驱动——分享 URL 挂载与解密列表

**What to build:** 以一条公开分享 URL(形如 `https://share.ente.io/c/<token>#<bs58 collectionKey>`)创建只读挂载:根目录列出整个相册解密后的文件名、明文大小与时间;驱动以 `enteshare` 注册并出现在管理端驱动列表(表单含 `share_url`、`endpoint`),Config 设 OnlyProxy + NoLinkURL + NoUpload。列表只走 `/public-collection/diff?sinceTime=` 分页:水位取页内 max(updationTime) 且不前进即终止;`isDeleted` 与墓碑(`encryptedData == "-"`)过滤;同名确定性 ` (2)` 后缀;文件名 `editedName || title`,时间 `editedTime || creationTime`(微秒转秒)。

**Blocked by:** 01

**Status:** ready-for-agent

- [x] httptest mock 下根目录列表返回解密后的名字/明文大小/时间,且仅发生按 collection 的 diff 端点调用(无 per-file 网络请求,断言请求序列)
- [x] 多页 diff 全量拉取;水位不前进时终止(mock 页外请求直接报错以断言不再翻页)
- [x] 已删除(isDeleted)与墓碑条目不出现在结果;同名文件获得插在扩展名前的确定性后缀
- [x] 请求头约定生效:`X-Auth-Access-Token`、`X-Client-Package: io.ente.photos`、UA 与官方 app 一致(无 HTTP client 默认 UA)
- [x] 驱动注册完成(含 all.go 空导入),写操作全部返回 not supported
