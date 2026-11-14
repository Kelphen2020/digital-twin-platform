# P38.0 API/运维变更（多副本导出任务领取）

## 1. 任务所有权与心跳

`export_jobs` 表新增两列（迁移 018）：

| 列 | 说明 |
|---|---|
| `owner` | 领取该任务的副本 id（`REPLICA_ID` 环境变量，缺省为 `hostname-pid`） |
| `heartbeat_at` | 最近一次心跳时间；用于识别宕机副本留下的孤儿任务 |

任务状态机：

```
pending --(claim: 原子 UPDATE ... WHERE status='pending')--> running
running --(heartbeat 每 N 秒)--> running
running --(超时无心跳)--> pending（回收，可被任意副本重新领取）
running --(完成)--> done / failed
```

- **领取原子性**：`UPDATE export_jobs SET status='running' ... WHERE id=$1 AND status='pending'`。
  两个副本同时抢占同一任务时，只有一行受影响，另一个副本直接放弃，**不会重复导出**。
- **心跳**：worker 运行期间每 `EXPORT_HEARTBEAT_SECONDS`（默认 10s）刷新一次；若发现租约已被抢走则主动放弃。
- **回收**：每副本每分钟扫描一次，把超过 `heartbeat × 6` 无心跳的 `running` 任务重置为 `pending`。

## 2. 新增环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `REPLICA_ID` | `hostname-pid` | 副本标识，写入 `export_jobs.owner` |
| `EXPORT_HEARTBEAT_SECONDS` | `10` | 心跳间隔 |

## 3. 行为变化

- 任务接口响应新增可选字段 `owner`、`heartbeat_at`。
- 后台导出不再继承 HTTP 请求上下文（此前请求返回后上下文被取消，导致 PG 模式下任务永远停在 `pending`）。

## 4. 多副本验证（真实 PostgreSQL）

| 场景 | 结果 |
|---|---|
| 两副本各建一个任务 | `replica-a` / `replica-b` 各执行一个，产物分别落在各自 `EXPORT_DIR` |
| 并发抢占同一任务 | 恰好一个赢家（`TestPostgresExportJobClaimRace`） |
| 副本宕机留下 `running` 任务 | 存活副本在约 35s 内回收为 `pending` 并重新执行 |
