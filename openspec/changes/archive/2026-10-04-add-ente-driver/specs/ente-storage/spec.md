## Purpose

为 OpenList(Go 主程序)提供 Ente(端到端加密相册服务)的只读挂载能力:通过公开分享 URL 或账号长期凭证浏览相册,并经强制代理流出解密后的明文文件与缩略图。

## ADDED Requirements

### Requirement: Share URL read-only mount
系统 SHALL 支持以一条 Ente 公开分享 URL(可配自托管 endpoint)创建只读挂载,根目录列出该相册全部文件,文件名/大小/时间为解密后的真实值。列表 MUST NOT 发出任何 per-file 网络请求;已删除项与墓碑项(`encryptedData == "-"`)MUST NOT 出现在列表中;同名文件 MUST 获得确定性的去重后缀。

#### Scenario: List a shared album
- **WHEN** 管理员以合法 Share URL 创建 EnteShare 挂载并请求根目录列表
- **THEN** 返回该相册全部文件的解密文件名、明文大小与时间,整个过程仅调用按 collection 的分页列表端点

#### Scenario: Tombstoned files are hidden
- **WHEN** 相册中某文件被移出(响应含墓碑)后请求列表
- **THEN** 该文件不出现在结果中

### Requirement: Account-level mount
系统 SHALL 支持以 Ente 账号的长期凭证(token + master_key,可选 secret_key)创建只读账号级挂载:根目录下每个 collection(相册)为一文件夹。隐藏相册 MUST 默认排除,`show_hidden` 开启时展示;缺少 secret_key 时共享相册 MUST 被跳过并告警,提供 secret_key 时其文件可列出。

#### Scenario: Albums as folders
- **WHEN** 以有效 token + master_key 请求根目录
- **THEN** 每个 collection 显示为一个文件夹,进入后列出其解密后的文件

#### Scenario: Hidden album toggle
- **WHEN** 账号存在隐藏相册且 `show_hidden` 未开启
- **THEN** 该相册不出现在根目录;开启后出现

### Requirement: Plaintext proxy download
Ente 驱动的文件与缩略图 MUST 以强制代理方式流出:驱动获取预签名 URL 后流式解密回传明文,不向客户端暴露任何直链或密文。下载响应 `Content-Length` MUST 取明文大小;Range 区间 MUST 由顺序解密满足(从流头解密丢弃前缀,seek 代价与 offset 成正比,v1 不做块级随机访问)。

#### Scenario: Download returns plaintext
- **WHEN** 下载 EnteShare 挂载中的任一文件
- **THEN** 收到的字节与该文件的原始明文逐字节一致,`Content-Length` 等于明文大小

#### Scenario: Range seek decrypts from stream head
- **WHEN** 播放器请求带非零起点 Range 头的视频
- **THEN** 返回该区间的明文字节,由驱动从密文流头部顺序解密至目标位置得出

### Requirement: Decrypted thumbnails
列表中的图片文件 SHALL 提供经解密的缩略图:对象携带指向本机代理路径的缩略图 URL,代理请求经同一解密管线流出明文小图,响应 MUST NOT 设置 Content-Length。

#### Scenario: Thumbnail renders in web UI
- **WHEN** 网页渲染 Ente 挂载中的图片列表
- **THEN** 缩略图请求返回解密后的明文小图

### Requirement: linkDeviceToken persistence
EnteShare 驱动 MUST 持久化 server 下发的 linkDeviceToken(挂载配置的 `device_token` 字段)并在后续请求中复用,重启后不重复占用设备位;token 刷新回写先例见 139/123_open 驱动。

#### Scenario: A refreshed device token survives a restart
- **WHEN** server 在响应头下发新的 linkDeviceToken 后挂载被重新加载
- **THEN** 后续请求携带该新 token,而非挂载配置中的旧值
