# IwaraZip Driver Spec

## Problem Statement

用户在 iwara.zip（YetiShare 文件托管服务）存有文件，但 OpenList 无法挂载它：没有对应驱动，用户无法像操作网盘一样浏览、下载、管理自己的 iwara.zip 文件。

## Solution

新增 `IwaraZip` 驱动。用户在账号设置页生成 key1/key2 两个 API key 填入 Addition，即可将 iwara.zip 账号的文件夹树挂载为 OpenList 目录树，支持完整读写：浏览（List）、直链解析（Link）、建文件夹、移动、重命名、复制、删除、上传。

## User Stories

1. 作为 OpenList 用户，我想把 iwara.zip 账号挂载为存储，以便在统一目录树中访问我的文件。
2. 作为 OpenList 用户，我想只填 endpoint/key1/key2 三项就能完成配置，以便无需注册流程即可使用。
3. 作为 OpenList 用户，我想浏览根目录和任意子文件夹，以便查看我账号下的全部内容。
4. 作为 OpenList 用户，我想复制文件或文件夹的下载直链，以便在其他工具中直接下载。
5. 作为 OpenList 用户，我想新建文件夹，以便整理我的文件。
6. 作为 OpenList 用户，我想上传文件，以便通过 OpenList 向 iwara.zip 存入内容。
7. 作为 OpenList 用户，我想移动/复制文件与文件夹，以便在文件夹间整理内容。
8. 作为 OpenList 用户，我想重命名文件与文件夹，以便修正命名。
9. 作为 OpenList 用户，我想删除文件与文件夹，以便清理空间。
10. 作为 OpenList 用户，我想在 token 过期后无需手动干预即可继续操作，以便长时间挂载稳定可用。
11. 作为 OpenList 用户，我想在 API 出错时看到来自 iwara 的原始错误信息，以便自行诊断（限额、权限、套餐限制）。
12. 作为 OpenList 用户，我想指定 root_folder_id 从任意子文件夹挂载，以便只暴露部分目录。

## Implementation Decisions

- iwara.zip 是 YetiShare 系文件托管服务，API 基址 `/api/v2/`，全部 POST，UTF-8。
- 认证：key1+key2 调 `/authorize` 换取 access_token + account_id；token 内存缓存，1 小时空闲过期或收到 401/_status=error 时自动重新获取。不持久化 token。
- 每个请求携带 access_token 与 account_id 两个参数。
- 目录树直接映射账号文件夹树：根 = parent_folder_id 为空（或 Addition 指定的 root_folder_id）。
- `/folder/listing` 一次返回整个文件夹的 folders[] 与 files[]，无分页；驱动直接消费。
- 下载为两步解析：文件 id → `/file/download` → 带 download_token 的签名 URL，作为 Raw URL 返回；签名短时效，不缓存。
- 上传：`/file/upload` multipart 整文件，无分块无断点续传；大小超套餐上限时错误透传。
- 错误处理：许多错误以 HTTP 200 + `_status:"error"` 返回，驱动必须检查响应体的 `_status` 字段而非仅 HTTP 状态码；错误信息（`response` 字段）透传给用户。429 不做等待重试。
- Addition 字段与 TypeScript 版（OpenList-Worker）保持一致：`endpoint`（默认 `https://www.iwara.zip`）、`key1`、`key2`（均必填）、`root_folder_id`。
- 不实现：远程 URL 导入（`/file/url_upload_add`）、分享管理、账号信息查询。

## Testing Decisions

- 只测外部行为：通过驱动对外接口（List/Link/Mkdir/Move/Rename/Copy/Remove/Put）对伪造的 iwara API（HTTP 层 mock）断言请求参数与结果映射，不断言内部实现。
- 重点用例：token 获取与过期重取、_status=error 的错误透传、文件夹/文件到目录树对象的映射、两步直链解析。
- 参考仓库内既有 driver 测试的写法与依赖约束（零外部测试依赖）。

## Out of Scope

- OpenList-Worker（TypeScript）侧的实现（另有独立 spec）。
- 通用 YetiShare 驱动（支持其他 YetiShare 站点）。
- 上传分块/断点续传、下载限速/等待处理、URL 导入、公共分享页面浏览。

## Further Notes

- 免费套餐存在 wait_between_downloads、download_speed、downloads_per_24_hours 限制，属站点侧行为，驱动不绕过。
