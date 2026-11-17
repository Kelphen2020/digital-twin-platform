# P39.0 变更（导出产物共享 + 并发迁移加固）

## 1. 可插拔的导出产物存储

新增包 `services/api/internal/exports`：

```go
type Store interface {
    Write(jobID string, data []byte) (int64, error)
    Read(jobID string) (io.ReadCloser, int64, error)
    Remove(jobID string) error
    Describe() string
}
```

- 默认实现 `LocalDir`：写入 `<EXPORT_DIR>/<jobID>.json`。
- **原子写入**：先写 `.json.tmp` 再 `rename`，读者永远不会看到半个文件。
- **路径校验**：拒绝空 id 与 `..`、`/`、`\` 等穿越路径（`TestLocalDirRejectsTraversal`）。
- 下载改为流式（`io.Copy` + `Content-Length`），大文件不再整体读入内存。

**多副本共享**：多个副本只要把 `EXPORT_DIR` 指向同一个共享目录（NFS / SMB / 挂载的
对象存储桶），即可互相下载彼此生成的产物——副本 A 执行的任务，可以直接从副本 B 下载。

`GET /api/tenants/{id}/overview` 的 `exports.artifacts` 字段会报告当前后端，例如 `dir:/data/exports`。

## 2. 并发迁移加固（真实缺陷修复）

多副本同时启动时，每个副本都会执行 DDL 迁移，第二个副本会撞上
`duplicate key value violates unique constraint "pg_type_typname_nsp_index"` 而**启动失败**。

修复：迁移在 PostgreSQL advisory lock（`pg_advisory_lock(4815162342)`）下串行执行，
后启动的副本会等待前者完成再检查/继续。

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 两副本同时启动（空库） | 一个成功、一个报 `duplicate key on pg_type` 退出 | ✅ 两个都成功 |
| 两副本同时启动（已迁移库） | 偶尔冲突 | ✅ 幂等，无冲突 |

## 3. 验证

| 项 | 结果 |
|---|---|
| 两副本共享 `EXPORT_DIR`，A 建任务、B 下载 | ✅ HTTP 200，3859 字节，内容完整（4 区域 / 6 相机 / 3 用户） |
| 两副本同时启动（空 PostgreSQL） | ✅ 均正常监听 |
| 单元测试 | `internal/exports` 3 个用例（往返 / 路径穿越 / 描述）全 PASS |
