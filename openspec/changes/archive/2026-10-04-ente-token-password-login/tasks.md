## Tasks

- [x] 1. `driver.go`：放宽 `Init` 分支为 `password != "" && (email != "" || token != "")` 走密码模式；password-only 返回明确错误；`initPasswordMode` fast path 失败且无 email 时返回可读错误（不尝试 SRP）
- [x] 2. `meta.go`：更新 `email`、`password`、`token`、`master_key` help 文案，说明三种凭证组合
- [x] 3. `driver_test.go`：补充分支测试——token+password 初始化成功、token 失效且无 email 报错、password-only 报错、存量 token+master_key 不受影响
- [ ] 4. 冒烟验证（ready-for-human）：用真实 token + password 配置初始化存储并列出文件
