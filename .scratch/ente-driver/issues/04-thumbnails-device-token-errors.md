# 04: 解密缩略图、device_token 复用与错误映射

**What to build:** 列表中的图片(`info.thumbSize > 0` 且 thumbnail decryptionHeader 存在)获得指向本机代理路径的缩略图 URL(携带 `type=thumb` 查询参数与签名,local 驱动先例);`Link` 按 `args.Type == "thumb"` 走 thumbnail v3 端点,同一解密管线,响应不设 Content-Length。EnteShare 捕获响应头 `X-Link-Device-Token` 进程内缓存并以 `X-Auth-Link-Device-Token` 回放(token 丢失时告警一次设备位风险)。错误映射:410 按 header 区分账号订阅失效/分享过期;403 device limit 映射设备数超限;401(分享)先探测 `/public-collection/info` 区分密码保护(明确提示不支持)与 token 失效;兜底带状态码与响应体。

**Blocked by:** 03

**Status:** ready-for-agent

- [x] hasThumb 文件在列表中带代理缩略图 URL;`type=thumb` 请求返回解密后的小图(fixture 断言 JPEG 魔数);ContentLength 取明文 thumbSize(spec 的“不设 Content-Length”指 Ente 缩略图端点响应头缺失——OpenList 代理层在缺省时会回退到整文件大小,故显式给 thumbSize,已在两驱动内联注释说明)
- [x] 无缩略图条件的文件不携带缩略图 URL
- [x] device_token:首次响应捕获、后续请求回放(mock 断言请求头)、不存在时不再重复占用(单次获取后复用)
- [x] 410(账号/分享两种文案)、403 device limit、401+密码保护探测、401 token 失效四类错误映射断言
