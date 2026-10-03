# Ente driver (OpenList Go)

Status: ready-for-agent

## Problem Statement

用户把照片存放在 Ente(端到端加密相册服务)。Ente 没有面向第三方的只读接入方式:官方 client 之外,没有任何办法列出相册、看到真实文件名、或下载到明文文件。用户希望像挂载其它网盘一样,把 Ente 挂进 OpenList(Go 主程序)浏览与下载。姊妹仓库 OpenList-Worker 已交付 TypeScript 版驱动,行为契约以其为准;本任务将其能力完整移植到 Go 主程序。

## Solution

新增两个只读驱动,共享同一套 Ente crypto 与 API client 模块:

- **EnteShare**:挂载一条公开分享 Share URL(零账号凭证)——解析 path 里的 share token 与 fragment 里的 collectionKey(bs58),经 `/public-collection/*` 端点列出并解密整个相册。v1 优先交付。
- **Ente**:账号级挂载——配置长期 `token` + `master_key`(可选 `secret_key` 以支持共享相册),根目录下每个 collection(相册)为一文件夹。

两者都作为强制代理驱动:下载与缩略图由 OpenList 拉取 server 下发的 S3 预签名 URL,用 fileKey 做 secretstream 流式解密后以明文流回传,不向客户端暴露直链。登录(SRP/passkey)外置,本仓库驱动只接受粘贴的长期凭证。

## User Stories

1. As an OpenList 管理员, I want 用一条 Ente 公开分享 URL 创建只读挂载, so that 无需交出任何账号凭证即可对外提供相册内容。
2. As an OpenList 管理员, I want 用 Ente 账号的 token + master_key 创建账号级挂载, so that 浏览账号下全部相册。
3. As a 挂载访问者, I want 相册显示为文件夹, so that 按 Ente 客户端的心智浏览。
4. As a 挂载访问者, I want 看到解密后的真实文件名、大小与时间, so that 不必打开文件就知道内容。
5. As a 挂载访问者, I want 下载得到明文文件, so that 直接可用。
6. As a 挂载访问者, I want 网页缩略图为解密后的小图, so that 图片挂载有正常的视觉体验。
7. As a 挂载访问者, I want 在网页播放器里直接播放视频, so that 无需下载(v1 拖动进度可用但代价是顺序解密到目标位置,较慢)。
8. As an OpenList 管理员, I want 共享给我的相册也能列出(提供 secret_key 时), so that 账号级挂载覆盖全部可见内容。
9. As an OpenList 管理员, I want 隐藏相册默认排除、可选展示, so that 与官方客户端一致。
10. As an OpenList 管理员, I want 自定义 Ente endpoint, so that 自托管 museum 也能挂载。
11. As an OpenList 管理员, I want token 长期自动保活, so that 不需要频繁重新配置(365 天未使用才过期)。
12. As an OpenList 管理员, I want 对密码保护的分享得到明确的不支持提示, so that 知道该分享无法挂载而非报错迷雾。
13. As an OpenList 管理员, I want owner 订阅失效(410)与设备数超限(403)映射为可读错误, so that 能向用户解释原因。
14. As an Ente 分享者, I want 我的 linkDeviceToken 被驱动持久化, so that 代理访问只占一个设备名额、不因出口 IP 变化或重启累积。
15. As a 挂载访问者, I want 同名文件获得确定性的去重后缀, so that 文件不互相覆盖。
16. As a 挂载访问者, I want 已删除相册、已移出相册的文件不出现在列表里, so that 列表与真实内容一致。
17. As a 挂载访问者, I want Live Photo 显示为照片与视频两个文件, so that 内容不丢失(v1 不做合并)。
18. As a 挂载访问者, I want 写操作(新建/删除/改名/上传)返回明确的 not supported 错误, so that 不误以为操作成功。
19. As an OpenList 管理员, I want 大相册分页拉全而不是截断, so that 列表完整。
20. As an OpenList 管理员, I want 回收站内容不展示, so that 与官方客户端一致。

## Implementation Decisions

- 双驱动拆分:`EnteShare`(`enteshare`)与 `Ente`(`ente`)各自注册为独立驱动包,共享的 crypto/client/util/types 模块放在 `ente` 驱动包内由两者复用,遵循仓库既有 `*_share` 惯例。不实现"离线索引"第三方案。
- 注册遵循仓库惯例:驱动工厂注册 + `drivers/all.go` 空导入,仅此两处;管理端配置表单由 Addition 反射自动生成,无需改动 OpenList-Frontend。
- 驱动 Config:`EnteShare` 与 `Ente` 均设 OnlyProxy + NoLinkURL + NoUpload(严格只读强制代理);`Ente` 根为账号 collection 列表,DefaultRoot `root`(对齐 worker 注册的 default_root)。
- 配置字段(Additional)与 worker 驱动逐字段一致(json tag 同名 snake_case,凭证在两实现间可直接迁移):
  - EnteShare:`share_url`(完整分享 URL)、`endpoint`(默认官方 `https://api.ente.com`)、`device_token`(隐藏字段,不进管理表单;server 下发 token 的持久化位)。
  - Ente:`endpoint`、`token`、`master_key`(hex/base64)、`secret_key`(可选;缺省时共享相册跳过并告警)、`show_hidden`(默认 false)。
- 密码学零新增依赖,全部落在既有直接依赖 `golang.org/x/crypto` 与标准库:XSalsa20-Poly1305 secretbox 开箱、Curve25519 sealed box 开箱(nonce = blake2b-24(epk‖recipientPK),按 Go 实现而非 libsodium)、XChaCha20 自定义 secretstream(以 ente CLI 官方 Go 实现为权威参考移植:4MB 明文块 + 17 字节尾部、TAG_FINAL 终结、nonce 前进规则)。bs58 解码用 math/big 自实现(仅解码,~30 行,测试向量锁定),不引入 base58 库。
- 请求约定:`X-Auth-Token`(账号)/ `X-Auth-Access-Token`(分享)、`X-Client-Package: io.ente.photos`、UA 与官方 app 一致(不含 HTTP client 默认 UA,以获得与官方 app 一致的 hot-DC 预签名行为);预签名 S3 URL 一律空 header 拉取;下载与缩略图一律用 v3 JSON 端点(响应 `{url}`),不用会跨服务重定向的 v1 307 端点。
- 列表策略:无状态全量拉取(collection 列表 + 按 collection 的 diff 循环翻页),复用 OpenList 既有 op 层列表缓存,驱动内不自建缓存;diff 水位取页内 `max(updationTime)`,水位不前进时终止以防 server 分页语义死循环;过滤 `isDeleted` 与 `encryptedData == "-"` 墓碑。
- 列表性能契约:`List` 只做按 collection 的 diff 端点调用,单次响应即含 fileKey 密文、metadata 密文与明文 `info.fileSize`/`updationTime`,文件名与时间在本地解密;禁止 per-file 网络请求(缩略图/下载是渲染时才触发的 lazy 请求,不计入 List)。
- 命名与时间:文件名取 `editedName || title`,冲突时确定性追加 ` (2)`(插在扩展名前);时间取 `editedTime || creationTime`(微秒转秒)。collection 名 `/` 替换为 `_`;隐藏相册按私有 magic metadata `visibility == 2` 判定,解密失败按非隐藏;共享相册 key 用 sealed box(`keyDecryptionNonce` 空值即共享),自有相册用 secretbox。
- 下载:驱动 `Link` 不返回 URL,返回 RangeReader 流式解密 + `ContentLength` 取明文 `info.fileSize`(与 NoLinkURL 语义配套);RangeRead 支持任意区间——从流头部顺序解密丢弃前缀再读目标区间,v1 不做块级随机访问索引(大 offset seek 代价 O(offset),已知上限,后续可评估前缀解密索引)。
- 缩略图:List 返回的对象内嵌缩略图 URL,指向本机代理路径并携带 `type=thumb` 查询参数与签名(遵循 local 驱动先例);`Link` 按 `args.Type == "thumb"` 分支走 thumbnail v3 端点,同一解密管线,缩略图响应不设 Content-Length。缩略图存在条件:`info.thumbSize > 0` 且 thumbnail decryptionHeader 存在。
- linkDeviceToken:EnteShare 以 Addition 既有 `device_token` 为初始值,请求携带 `X-Auth-Link-Device-Token` 回放;捕获响应头 `X-Link-Device-Token` 变化时更新 Addition 字段并经 `op.MustSaveDriverStorage` 持久化(token 刷新回写先例:139/123_open 驱动),重启后不重复占用设备位。
- 错误映射:410 按 header 区分账号订阅失效/分享过期;403 device limit 映射设备数超限;401(分享)先探测 `/public-collection/info` 区分密码保护(不支持,明确提示)与 token 失效;均转成可读的驱动级错误,兜底带状态码与响应体。
- 写操作:不实现任何写接口,全部返回明确的 not supported(NoUpload)。
- 行为蓝本:OpenList-Worker 仓库(Go 侧逐条镜像)——驱动实现 `src/backend/drivers/ente/*` 与 `ente_share/*`、驱动文档 `docs/drivers/ente.md`、决策记录 `docs/adr/0001`/`0002`;测试向量可直接复用其 `crypto.test.ts` 内联的 ente 官方向量与 Go 交叉生成向量(生成侧即 Go)。

## Testing Decisions

- 只测外部行为,不测实现细节;测试零外部依赖、可在所有支持平台运行,随 `go test ./drivers/...` 执行。
- 最高既有 seam:驱动 `List`/`Link` 方法,对 httptest mock server(替换全局共享 HTTP client 的仓库既有注入模式,teldrive 驱动测试为先例)+ 真实 crypto 模块 + 预生成密文 fixture,断言解密后的名字/大小/时间/下载流。
- crypto 模块单测用固定测试向量:secretbox(ente 官方向量)、secretstream(多块顺序/乱序拒绝/篡改拒绝/单块 TAG_FINAL metadata)、sealed box;向量复用 OpenList-Worker 已交叉验证的 fixture,防止移植漂移。
- mock server 按 sinceTime 索引分页字典,页外请求直接报错以断言分页终止;用任意小分片喂入解密流,断言与网络分片边界解耦。
- 真实网络冒烟仅以 env-gated 手动测试(真实 share URL/token 经环境变量注入)形式提供,不进 CI。

## Out of Scope

- 一切写操作(上传、删除、移动、改名)。
- Range 的块级随机访问优化(顺序解密丢弃前缀已是正确实现,性能优化留待后续)。
- 密码保护分享相册(64MB argon2,留作后续)。
- file-link 单文件分享挂载。
- 仓库内用户名密码/SRP 登录(登录属 OpenList-APIPages 范畴;`.scratch/ente-driver/ente-ref/` 的 SRP 素材仅供该侧参考,不入库、本驱动不使用)。
- 回收站、"全部照片"虚拟目录、Live Photo 合并。

## Further Notes

- 术语沿用 worker 仓库约定:collection(相册)、Share URL(公开分享链接,path 携带 token、fragment 携带 bs58 编码的 collectionKey)、linkDeviceToken(server 下发的设备 JWT)、墓碑(`encryptedData == "-"` 的已移出条目)。
- Ente 端点、密钥层级、分页语义、设备数机制等完整调研见 OpenList-Worker 仓库文档;实现时以 worker `src/backend/drivers/ente/*` 实际代码为逐条对照蓝本。
