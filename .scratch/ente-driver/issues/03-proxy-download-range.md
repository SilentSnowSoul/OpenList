# 03: 代理下载——RangeReader 流式解密

**What to build:** 在 EnteShare 挂载里下载文件得到明文:向 v3 JSON 端点(`/public-collection/files/download/v3/<fileID>`,响应 `{url}`)取 S3 预签名 URL,空 header 拉取后用 fileKey 做 secretstream 流式解密回传。驱动 `Link` 不返回 URL,返回 RangeReader + ContentLength(明文 `info.fileSize`);RangeRead 支持任意区间——从流头部顺序解密丢弃前缀再读目标区间(密文块边界 4MB+17,与网络分片解耦);TAG_FINAL 后关流,流提前结束报错。

**Blocked by:** 02

**Status:** ready-for-agent

- [x] fixture 密文经下载管线解出的字节与明文逐字节一致;任意小分片喂入解密流仍正确(与网络分片边界解耦)
- [x] Link 无 URL 且 ContentLength 等于明文大小(NoLinkURL 语义配套)
- [x] RangeRead 支持 start>0 区间(顺序丢弃前缀)与全量读取;seek 代价 O(offset) 以注释标注已知上限
- [x] 仅使用 v3 JSON 端点(不用 v1 307);预签名 URL 拉取不带任何 ente 头(mock 断言)
- [x] 大文件流式转发,不在内存中整体缓冲
