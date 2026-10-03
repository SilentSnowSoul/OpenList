# 03: 写操作（Mkdir/Move/Rename/Copy/Remove）

**What to build:** 用户可在 IwaraZip 挂载内新建文件夹、移动/复制/重命名/删除文件与文件夹，操作结果立即反映在列表中。

**Blocked by:** 01: IwaraZip 驱动骨架 + 认证 + List

**Status:** resolved

- [x] MakeDir → /folder/create；Remove → /file/delete + /folder/delete；Move → /file/move + /folder/move；Rename → 对应 edit 接口；Copy → /file/copy（文件夹无 copy 则明确报 NotImplement）
- [x] 单元测试覆盖各操作的正确 API 参数组装
