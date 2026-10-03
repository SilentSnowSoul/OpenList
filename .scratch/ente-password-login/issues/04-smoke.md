# 04 — 端到端冒烟验证

Status: ready-for-human
Type: task
Blocked by: 03

## 目标

对真实（或自建 museum）Ente 帐号冒烟：

- 密码模式挂载，List / 读文件正常，数据库检查无 master key 明文。
- 吊销 token 后 reload，自动 SRP 重登并更新 token。
- 存量凭证模式存储不受影响。
