# P42.0 变更（事件直方图下推到存储层）

## 背景

P41 的概览柱状图在 API 进程内用 `ListEvents(limit=100000)` 取回窗口内全部事件再分桶，
大租户（单窗口 10 万+ 事件）会带来明显的内存与延迟开销。

## 变更

`Store` 接口新增：

```go
EventHistogram(ctx context.Context, from, to time.Time, bucket time.Duration) ([]model.HourStat, error)
```

- **PostgreSQL 实现**：用 `date_bin(make_interval(secs => $4), occurred_at, $1)` 在数据库内聚合，
  只返回 `桶 + 计数`，API 进程不接触明细行。
- **内存实现**：遍历租户事件计数（与 PG 结果逐桶一致，有契约测试保证）。
- 两者都会**零填充空桶**，返回连续时间轴，最多 400 个桶。

## 影响

| 项 | 之前 | 之后 |
|---|---|---|
| 数据量 | 窗口内全部事件（最多 10 万行） | 桶数（8–25 行） |
| 聚合位置 | API 进程 | 数据库 |
| 内存占用 | 与事件数成正比 | 常数 |

对外 API（`GET /api/tenants/{id}/overview` 的 `series` / `bucket_seconds`）**完全兼容**，前端无需改动。
