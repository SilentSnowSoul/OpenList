# 04: Put 上传

**What to build:** 用户经 OpenList 向 IwaraZip 挂载的任意文件夹上传文件，上传成功后文件出现在目标文件夹列表中；超套餐上限/失败时原始错误透传。

**Blocked by:** 01: IwaraZip 驱动骨架 + 认证 + List

**Status:** resolved

- [x] Put 走 /file/upload multipart 整文件，无分块无断点续传
- [x] 上传后 List 可见新文件（响应对象直接映射；后续 List 从 API unpaginated 返回上传文件）
- [x] 单元测试覆盖 multipart 组装与错误透传
