# P9.0 性能压测报告（Load & Backpressure）

> 本文记录 P9.0 的压测工具、基准矩阵、`/metrics` 采样与结论。
> **所有数字均为单节点开发笔记本上的实测值，不是专用压测机结果**；机器上还并行运行着 Web dev server / 浏览器，
> 绝对值仅用于本 P9 demo 的容量判断与回归对比。

## 1. 环境与配置

| 项 | 值 |
|---|---|
| 机器 | macOS arm64（单节点笔记本，8 核级） |
| 客户端 | Python 3.14.2，`scripts/load.py`（stdlib，10 线程） |
| 服务端 | Go API 单进程 `:8080`（本地构建），内存 store |
| 鉴权 | `AUTH_MODE=on`（压测用 admin token；ingest 保持机器入口免 token） |
| 事件总线 | `BUS_DRIVER=jsonl`（`data/bus/events.jsonl`） |
| P9 默认参数 | `HUB_CLIENT_BUFFER=256`、`WS_TRACK_SAMPLE=1`、`INGEST_RATE_LIMIT=0`、`INGEST_BURST=200`、`MAX_BODY_BYTES=1048576` |
| 其他 | 通知默认关闭；升级 `after_seconds=300`；矩阵期间无值班派单；有 1 个 WebSocket 客户端在线（Web dev） |
| 时间 | 2026-11-08 22:06（Asia/Shanghai） |

## 2. 压测工具与命令

`scripts/load.py` 在保留原有参数（`--qps/--duration/--workers/--track-ratio/--api-token/--ingest-token`）的基础上新增：

| 参数 | 说明 |
|---|---|
| `--ramp` | 线性爬坡：QPS 从目标的 25% 升到 100%（平均 = 62.5% 目标；通过阈值按平均速率计算） |
| `--read-ratio R` | 0–1，R 比例的调度槽位改发 `GET /api/events?limit=20` 读请求（其余为 ingest；AUTH_MODE=on 时读请求需 Bearer token） |
| `--report PATH` | 每次运行向 Markdown 表格追加一行（同一 PATH 连续运行即得到矩阵表） |
| `--json PATH` | 每次运行向 JSON 数组追加一个对象（目标/达成/延迟分位/计数/`/metrics` 增量/错误样本） |

每次运行会自动抓取 `/metrics` 前后快照并给出计数器增量（ws frames/dropped、track points、ingest rejected、ingest duration、bus 计数）。

```bash
# 获取压测用 admin token（ingest 入口本身免 token）
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@123456"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

# 基准矩阵（--report/--json 共享文件以累积矩阵与明细）
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 50  --duration 5 --workers 10 --seed 11 --report /tmp/perf_matrix.md --json /tmp/perf_runs.json
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 100 --duration 5 --workers 10 --seed 12 --report /tmp/perf_matrix.md --json /tmp/perf_runs.json
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 200 --duration 5 --workers 10 --seed 13 --report /tmp/perf_matrix.md --json /tmp/perf_runs.json
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 100 --duration 5 --workers 10 --read-ratio 0.3 --seed 14 --report /tmp/perf_matrix.md --json /tmp/perf_runs.json
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 200 --duration 10 --workers 10 --ramp --seed 15 --report /tmp/perf_matrix.md --json /tmp/perf_runs.json

# 限流验证（服务端 INGEST_RATE_LIMIT=10 / INGEST_BURST=200 时）
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 100 --duration 5 --workers 10 --seed 21
```

> 说明：`load.py` 使用 `urllib` **每个请求新建 TCP 连接（无 keep-alive）**，因此 HTTP 延迟包含连接建立开销；
> 服务端真实 handler 成本见 §5 的 `ingest_duration_seconds`（平均约 47 µs）。

## 3. 基准矩阵（steady，ingest 为主）

固定 10 worker、`--track-ratio 0.5`、允许 5 s 去重窗口生效：

| # | 模式 | 目标 QPS | 时长 | 调度请求 | 成功 | 失败 | 达成 QPS | p50 ms | p95 ms | p99 ms | mean ms | max ms | accepted | dropped | events | tracked | ws_frames+ | ws_dropped+ | rejected+ | bus_pub+ | 结果 |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | steady | 50 | 5 s | 250 | 250 | 0 | 50.00 | 0.57 | 0.77 | 1.10 | 0.60 | 1.60 | 25 | 225 | 25 | 126 | +19 | 0 | 0 | +25 | PASS |
| 2 | steady | 100 | 5 s | 500 | 500 | 0 | 100.00 | 0.56 | 2.07 | 3.40 | 0.77 | 4.78 | 27 | 473 | 27 | 261 | +289 | 0 | 0 | +27 | PASS |
| 3 | steady | 200 | 5 s | 1000 | 1000 | 0 | 200.00 | 0.52 | 2.68 | 6.54 | 0.90 | 17.16 | 29 | 971 | 29 | 500 | +530 | 0 | 0 | +29 | PASS |
| 4 | steady + read 0.3 | 100 | 5 s | 500 | 500 | 0 | 100.00 | 0.59 | 3.39 | 6.19 | 1.08 | 16.03 | 26 | 301 | 26 | 172 | +200 | 0 | 0 | +27 | PASS |
| 5 | ramp 25%→200 | 200（峰） | 10 s | 1250 | 1250 | 0 | 125.00（平均） | 0.53 | 2.59 | 6.20 | 0.88 | 12.99 | 56 | 1194 | 56 | 629 | +687 | 0 | 0 | +56 | PASS |

- 第 4 行：500 请求中 173 个是 `GET /api/events`（`read_ratio=0.3`），读请求 173/173 成功；327 个 ingest。
- `achieved QPS` 按“成功请求数 / 运行窗口”计算；ramp 行的通过阈值按平均速率（125 QPS）的 80% 判定。
- `accepted/dropped` 来自 ingest 响应；稳态下事件数被 5 s 去重窗口限制（每 camera+class 每 5 s 至多 1 个事件），
  因此 50→200 QPS 的 `accepted` 稳定在 25–29，而 `dropped` 随 QPS 线性增长（被抑制的重复检测）。
- `ws_frames+` 矩阵期间有 1 个 Web 客户端在线，包含 `track_point` / `stats` 等帧；`ws_dropped=0`。

## 4. Ramp 曲线

`--ramp` 模式下到达率 `λ(t) = target × (0.25 + 0.75·t/D)`，累计请求数
`C(u) = N·(0.4u + 0.6u²)`（`u=t/D`），`N = 0.625 × target × D`。
200 QPS/10 s 的 ramp 运行共 1250 请求，平均 125 QPS，全程 0 失败；p99 6.20 ms，说明从 50 QPS 爬升到 200 QPS
期间系统无排队恶化（分位与固定 200 QPS 运行接近）。

## 5. `/metrics` 快照（矩阵运行前后）

| 指标 | 运行前 | 运行后 | 增量 |
|---|---:|---:|---:|
| `dsh_twin_detections_total` | 220 | 3548 | +3328 |
| `dsh_twin_events_total`（info+warn+critical） | 57 | 221 | +164 |
| `dsh_twin_ingest_duration_seconds_sum` | 0.011957 | 0.170075 | +0.158118 |
| `dsh_twin_ingest_duration_seconds_count` | 220 | 3548 | +3328 |
| `dsh_twin_ws_frames_total` | 0 | 1725 | +1725 |
| `dsh_twin_ws_dropped_total` | 0 | 0 | 0 |
| `dsh_twin_track_points_written_total` | 96 | 1784 | +1688 |
| `dsh_twin_track_points_broadcast_total` | 96 | 1784 | +1688 |
| `dsh_twin_ingest_rejected_total` | 0 | 0 | 0 |
| `dsh_twin_bus_published_total` | 57 | 221 | +164 |
| `dsh_twin_bus_replayed_total` | 0 | 0 | 0 |
| `dsh_twin_bus_errors_total` | 0 | 0 | 0 |
| `dsh_twin_bus_lag` | 1397 | 1561 | +164 |
| `dsh_twin_ws_clients` | 0 | 1 | +1（Web 客户端接入） |

- 服务端 ingest handler 平均耗时 = `0.158118 s / 3328 ≈ 47.5 µs`；矩阵期间 HTTP p50 仅 0.5–0.6 ms，
  延迟主要来自客户端每请求新建连接、线程调度与 Go net/http 开销，而非业务处理。
- `track_points_written == track_points_broadcast`（`WS_TRACK_SAMPLE=1`，未采样丢弃）；
  `ws_dropped=0`（矩阵只有一个 WS 客户端，`HUB_CLIENT_BUFFER=256` 足够）。
- `bus_published` 增量与事件增量一致（164），JSONL 驱动写入正常，`bus_errors=0`。

## 6. 429 限流验证（`INGEST_RATE_LIMIT=10`，`INGEST_BURST=200`）

服务端临时以 `INGEST_RATE_LIMIT=10` 重启（BURST 默认 200），客户端跑 `100 QPS × 5 s`（500 请求）：

| 指标 | 值 |
|---|---|
| 调度请求 | 500 |
| 成功（202） | 248（49.6%） |
| 失败（429） | 252 |
| 达成 QPS | 49.60（目标 100，阈值 80 → 预期 FAIL 退出码 1） |
| HTTP 延迟（完成请求） | p50 0.53 ms / p95 2.67 ms / p99 5.01 ms / mean 0.88 ms / max 10.22 ms |
| accepted / dropped / events | 27 / 221 / 27 |
| `dsh_twin_ingest_rejected_total` | 0 → 252（+252） |
| `dsh_twin_detections_total` | 0 → 249 |
| `dsh_twin_bus_published_total` | +27 |

错误样本（前 5 条均一致）：

```
ingest camera=CAM-* HTTP 429: {'error': 'rate_limited', 'message': 'ingest rate limit exceeded'}
```

结论：令牌桶按预期工作（突发 200 + 10/s 补充 ≈ 248 通过），超限请求返回 429 并计入
`dsh_twin_ingest_rejected_total`；`load.py` 将 429 视为失败并以退出码 1 结束（符合压测工具的判定语义）。
验证完成后服务端已恢复默认（`INGEST_RATE_LIMIT=0`，计数器清零）。

## 7. 观测到的瓶颈与限制

1. **单进程 Go API 在 ≤200 QPS ingest 下无压力**：0 失败、p99 ≤ 6.54 ms、handler 平均 ~47 µs；
   200 QPS 时 max 17 ms 的尖峰来自 Go GC/调度与客户端线程抖动，不构成排队恶化（ramp 运行同样 0 失败）。
2. **事件扇出被去重窗口限制**：`accepted` 在 50–200 QPS 均约 25–29/5 s（camera+class 5 s 窗口），
   因此总线发布、WS 事件帧、通知投递的数量远小于 ingest 数量；高 QPS 下主要成本是检测解析与 track point 写入。
3. **JSONL 总线**：每个事件追加一行；矩阵期间 `bus_published +164`、`bus_errors=0`；
   事件率受去重限制，写文件不构成瓶颈（若调大去重窗口或关闭去重需重新评估 I/O）。
4. **WS 背压未被压测覆盖**：矩阵只有 1 个 WS 客户端，`ws_dropped=0`；多客户端/慢客户端场景需观察
   `dsh_twin_ws_dropped_total` 并调大 `HUB_CLIENT_BUFFER`。
5. **客户端本身也是限制**：Python `urllib` 每请求新建连接，10 线程下 200 QPS 客户端 CPU/连接开销明显；
   再往上压测建议使用 keep-alive 客户端或更多 worker。
6. **共享环境**：矩阵期间 Web dev/浏览器在线，数字包含其干扰；绝对容量结论请以专用压测机复测为准。
7. 本次矩阵未覆盖：>200 QPS 稳态、多 WS 客户端背压、`notify=true` 通知风暴、PostgreSQL/持久化存储模式。

## 8. 调优建议

| 参数/项 | 建议 |
|---|---|
| `INGEST_RATE_LIMIT` / `INGEST_BURST` | 演示/共享环境建议开启（如 10/200 已实测返回 429）；单机专用压测可关闭（0）以测真实上限；生产按 SLO 设限并监控 `dsh_twin_ingest_rejected_total`。 |
| `WS_TRACK_SAMPLE` | 浏览器多或网络差时调为 2–4（仅降低 broadcast；`track_points_written` 落库不受影响），配合前端轨迹抽稀。 |
| `HUB_CLIENT_BUFFER` | 默认 256 在单客户端下 0 丢弃；若 `dsh_twin_ws_dropped_total` 增长，调到 512–2048 或对慢客户端降级。 |
| `BUS_DRIVER` | 需要回放/持久化用 `jsonl`（本矩阵）；纯秒级 demo 追求吞吐可用 `memory`。 |
| `MAX_BODY_BYTES` | 1 MiB 对当前 1–3 检测/批的负载余量充足；大批量检测时再调高。 |
| 压测客户端 | 生产化压测改用 keep-alive；本机 10 worker 足以覆盖 200 QPS，再高需加 worker/多机。 |
| 复测方法 | `--report/--json` 累积矩阵与明细；同一机器静默环境重复 3 次取中位数。 |

## 9. 复现与产物

- 工具：`scripts/load.py`（本报告所有数字均由其生成）。
- 本次会话产物（临时文件，不提交仓库）：`/tmp/perf_matrix.md`（Markdown 表格）、`/tmp/perf_runs.json`（逐次 JSON 明细）、
  `/tmp/perf/metrics_before.txt`、`/tmp/perf/metrics_after.txt`、`/tmp/perf/metrics_ratelimit_{before,after}.txt`。
- 相关文档：`docs/API_P9.md`（运行时配置/指标契约）、`services/simulator/README.md`（`load.py` 用法）。
- 结论：**P9 目标达成** —— 单节点笔记本上 ingest 50/100/200 QPS 稳态与 25%→200 QPS 爬坡全部 100% 成功，
  p99 ≤ 6.5 ms；通知/WS 背压与限流机制具备可观测指标并在 INGEST_RATE_LIMIT=10 下验证了 429 + 拒绝计数。
