# 06: 冒烟测试与文档收尾

**What to build:** 收尾:env-gated 真实网络冒烟(真实 Share URL / 账号凭证经环境变量注入,env 缺失时 skip,不进 CI);全仓库构建与静态检查绿灯;面向管理员的两驱动挂载配置说明(挂载参数、凭证如何获得、只读与代理行为、已知限制:seek 代价、密码保护分享不支持)。

**Blocked by:** 05

**Status:** ready-for-agent

- [x] env-gated 冒烟测试存在且 env 缺失时 skip(`drivers/ente/smoke_test.go`、`drivers/ente_share/smoke_test.go`,env 注入 share URL / token+master_key,覆盖 list/下载/缩略图);提供真实挂载下 list/下载/缩略图的人工验证清单(见各文件头部注释中的命令)
- [x] `go build ./...` 与 `go vet`(新驱动包)全绿;`go test ./drivers/...` 中 ente/ente_share 全绿——123/189/189pc/chaoxing/google_drive/google_photo/lanzou/onedrive_sharelink 的 vet 失败为 HEAD 既有(非本驱动包),已用干净 worktree 在 `ea10624f` 上复核同样失败
- [x] 配置说明覆盖两驱动的全部 Additional 字段与凭证获取指引(登录外置,凭证由 Ente 官方 client/后续 APIPages 登录页产出):`OpenList-Docs/pages/guide/drivers/ente.md`
