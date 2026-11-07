# 数字孪生安防平台 · Web 前端（P1.0）

Vite + Vue 3 + TypeScript + Three.js 实现的三维安防客户端：400 m × 400 m 场景、相机视锥、HLS 视频、实时轨迹、事件上图、告警处置、统计看板与单应标定。契约见 `docs/API.md`（P0）与 `docs/API_P1.md`（P1，向后兼容）。

## 快速开始

```bash
cd digital-twin-platform/apps/web
pnpm install
pnpm dev          # http://127.0.0.1:5173
```

其他脚本：

| 命令 | 说明 |
| --- | --- |
| `pnpm dev` | 开发服务器，固定端口 5173（`strictPort`） |
| `pnpm build` | 类型检查（`vue-tsc --noEmit`）+ 生产构建到 `dist/` |
| `pnpm preview` | 预览构建产物，端口 4173（同样代理 `/api`、`/ws`） |
| `pnpm typecheck` | 仅类型检查 |

要求 Node.js `^20.19` 或 `>=22.12`（Vite 8 要求），pnpm 10。运行时依赖：`vue`、`three`、`hls.js`（按需动态加载）。

## 环境变量

在 `apps/web` 下创建 `.env.local`（不入库）或直接使用 shell 环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE` | 空（同源） | 后端源站，如 `http://127.0.0.1:8080`。为空时前端请求同源 `/api`、`/ws`，由 Vite 代理到 `127.0.0.1:8080`；设置后前端直接请求该源站，Vite 代理目标也改为该地址。仅写协议 + 主机 + 端口。 |
| `VITE_SITE_NAME` | `数字孪生安防平台` | 顶栏站点标题。 |
| `VITE_API_TOKEN` | 空（鉴权关闭） | 可选鉴权（docs/API_P1.md §7）：REST 请求携带 `Authorization: Bearer <token>`，WebSocket 追加 `?token=<token>`。 |

默认配置无需任何环境变量即可与本地 Go API（`http://127.0.0.1:8080`）联调。

## 功能与交互

### 1. 三维场景（Three.js）

- 400 × 400 m 地面网格；两栋建筑（A 栋 ENU 中心 `-40,40`、`60×40×20 m`；B 栋 `60,-20`、`50×30×14 m`）；渐变天穹 + 雾；环境光 / 半球光 / 主平行光（阴影）/ 冷色补光。
- 相机标记：发光球体 + 按 `heading`/`fov_h`/`range_m` 生成的半透明视锥，颜色随状态（在线绿 / 离线红 / 异常琥珀）；悬停显示编号、名称、状态与参数，点击选中并高亮。
- 事件标记：世界坐标处扩散脉冲环 + 光柱 + 顶部标记体，按 `severity` 着色，约 60 s 后自动淡出（事件流仍保留历史）。
- 轨迹：选中相机的历史轨迹与实时 `track_point` 增量，按 `track_id` 绘制折线，按检测 class 着色，旧点向背景色淡出；顶栏「显示轨迹」全局开关（默认开）。
- OrbitControls 旋转/平移/缩放；左下角 FPS 与操作提示，右下角颜色图例。

### 2. HLS 视频（相机详情）

- 选中相机时播放其 `stream_url`（P1 演示流 `/hls/test.m3u8`，由 `scripts/make_hls.sh` 生成到 `apps/web/public/hls/`，Vite 直接托管）。
- 浏览器原生支持 HLS（Safari/iOS）时用原生 `<video>`；其余浏览器按需 `import('hls.js')` 播放，不增加首屏包体。
- LIVE 徽标、清晰度徽标、播放/暂停、静音切换、加载中 / 失败重试状态；相机切换与组件卸载时销毁 hls 实例并清理视频源。
- `stream_url` 为空时显示明确占位文案。

### 3. 事件处置（P1）

- 点击事件流条目或三维事件标记打开左侧处置面板：事件信息、状态（待处置 / 已确认 / 已关闭）、处置动作按钮 **确认 / 关闭事件 / 派单**。
- 关闭可填写备注（`POST /api/events/{id}/close`）；派单需填写目标与可选备注（`POST /api/events/{id}/dispatch`），不改变事件状态。
- 通过 `GET /api/events/{id}` 展示审计动作时间线（ack / close / dispatch、actor、target、note、时间）。
- 监听 WS `event_updated` 帧实时刷新列表与处置详情。
- 事件流支持类型 / 等级筛选、状态徽标与「导出 CSV」按钮（`GET /api/stats/events.csv`，带当前统计时间范围与鉴权头）。

### 4. 统计看板（右侧「统计」页签）

- `GET /api/stats/summary?from=&to=`：事件总数、按严重程度 / 类型分布、高发相机 TOP、按小时柱状图（纯 CSS）。
- 时间范围切换（近 1 小时 / 近 24 小时），打开页签自动加载、每 30 s 自动刷新，支持手动刷新与导出 CSV。

### 5. 相机标定（相机详情 → 管理标定）

- 展示「已标定（RMSE/点数/时间）」或「未标定」。
- 标定弹窗：粘贴 4–20 组控制点 JSON（`px/py` 像素 → `x/y` ENU 米），`POST /api/calibration/solve` 求解并展示 3×3 单应矩阵与残差；`PUT /api/cameras/{id}/calibration` 保存；支持删除标定（`DELETE`）。

### 6. 连接与降级

- 启动拉取 `GET /api/cameras` + `GET /api/events?limit=200`；失败时显示离线横幅并每 10 s 自动重试。
- WS `/ws` 消费 `hello` / `event` / `event_updated` / `camera_status` / `track_point` / `stats`，指数退避重连（1 s → 30 s 封顶）。
- 后端不可用时场景与轨道控制仍可浏览，数据区域显示空状态。

## 后端契约（前端实际调用）

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/health`（仅人工/冒烟） | 健康检查 |
| GET | `/api/cameras` | 相机台账（含 `stream_url`、`calibration`） |
| GET | `/api/events?limit=200` | 事件列表 |
| GET | `/api/events/{id}` | 事件详情 + 处置动作时间线 |
| POST | `/api/events/{id}/ack` | 确认 |
| POST | `/api/events/{id}/close` | 关闭（可带备注） |
| POST | `/api/events/{id}/dispatch` | 派单（目标 + 备注） |
| GET | `/api/tracks?camera_id=&limit=500` | 历史轨迹点 |
| GET | `/api/stats/summary?from=&to=` | 统计汇总 |
| GET | `/api/stats/events.csv?from=&to=` | 事件 CSV 导出 |
| POST | `/api/calibration/solve` | 单应矩阵求解 |
| PUT/DELETE | `/api/cameras/{id}/calibration` | 保存 / 删除标定 |
| WS | `/ws` | `hello/event/event_updated/camera_status/track_point/stats` |

## 坐标与渲染约定

- 数据坐标：局部 ENU 米制（x 东、y 北、z 上），见 `docs/API.md`。
- Three.js 映射：`three.x = ENU.x`，`three.y = ENU.z`，`three.z = -ENU.y`（`src/scene/coordinates.ts`）。
- `heading`：正北 0°、顺时针；映射到 Three.js 水平方向为 `(sin h, 0, -cos h)`。
- 视锥：`ConeGeometry` 平移使顶点位于相机位置，局部 +Z 对准 heading；锥长取 `range_m`（夹取 18–90 m）。
- 轨迹点绘制于地面以上 0.4 m；3 分钟窗口外接近背景色，单轨最多 300 点 / 场景最多 40 条。

## 目录结构

```
apps/web/
├─ index.html
├─ vite.config.ts                # 端口 5173；/api、/ws 代理；VITE_API_BASE 覆盖
├─ public/hls/                   # P1 演示 HLS（由 scripts/make_hls.sh 生成，不入库）
├─ src/
│  ├─ main.ts / App.vue          # 应用入口与三栏布局（场景 / 相机+视频 / 事件流+统计页签）
│  ├─ types.ts                   # P0 + P1 数据模型与 WS 消息类型
│  ├─ labels.ts                  # 中文标签、状态/等级/轨迹颜色、时间格式化
│  ├─ api.ts                     # REST 客户端（可选 Bearer 鉴权 + CSV Blob 下载）
│  ├─ ws.ts                      # WS 客户端（消息校验 + 指数退避 + 可选 token）
│  ├─ store/twin.ts              # 响应式 store（相机/事件/轨迹/详情/统计/选择状态）
│  ├─ components/
│  │  ├─ TopBar.vue              # 站点、在线相机、事件总数、FPS、轨迹开关、WS 状态、时钟
│  │  ├─ CameraDetail.vue        # 相机详情 + 视频 + 标定入口
│  │  ├─ CameraVideo.vue         # HLS 播放器（原生 / hls.js、LIVE、错误重试、切换清理）
│  │  ├─ CalibrationDialog.vue   # 控制点求解 / 保存 / 删除
│  │  ├─ EventFeed.vue           # 事件流、筛选、状态徽标、确认、CSV 导出
│  │  ├─ EventDetail.vue         # 事件处置面板（确认/关闭/派单 + 动作时间线）
│  │  ├─ StatsPanel.vue          # 统计看板（1h/24h、分布、TOP 相机、小时柱状图）
│  │  └─ OfflineBanner.vue       # 后端不可用横幅 + 重试
│  ├─ scene/                     # TwinScene + 环境/相机标记/事件标记/轨迹/坐标/资源释放
│  └─ styles/main.css            # 深蓝指挥中心风格（1280–1920 适配）
└─ README.md
```

## 哪些是 Mock，哪些是实时数据

| 内容 | 来源 |
| --- | --- |
| 地面网格、两栋建筑、天空/雾、灯光 | **前端静态 Mock**（P0 固定布局；P1 不接真实模型/标定数据） |
| HLS 测试视频 | **本地演示流**：`scripts/make_hls.sh` 用 ffmpeg 生成到 `apps/web/public/hls/`（VOD 测试片源，UI 固定显示 LIVE 徽标）；真实接入时把 `stream_url` 换成 ZLMediaKit 等输出的 WebRTC/FLV/HLS 地址 |
| 相机台账、状态、视频地址、标定 | **实时**：`GET /api/cameras` + WS `camera_status` + 标定 API |
| 事件列表、事件上图、状态与处置 | **实时**：REST + WS `event` / `event_updated` + ack/close/dispatch API |
| 轨迹 | **实时**：`GET /api/tracks` + WS `track_point`（由模拟器产生） |
| 统计与 CSV | **实时**：`/api/stats/summary`、`/api/stats/events.csv` |
| AI 推理 | **未接入**，由 `services/simulator` 按 `/api/ingest/detections` 契约模拟 |

## 验证方式

```bash
# 构建
cd digital-twin-platform/apps/web
pnpm install && pnpm typecheck && pnpm build

# 启动（后端 :8080 已运行）
pnpm dev

# 代理与页面
curl -s http://127.0.0.1:5173/ | grep -q 'id="app"' && echo "app shell ok"
curl -s http://127.0.0.1:5173/api/health
curl -s "http://127.0.0.1:5173/api/tracks?camera_id=cam_001&limit=5"
curl -s "http://127.0.0.1:5173/api/stats/summary?from=2026-01-01T00:00:00Z&to=2030-01-01T00:00:00Z" | head -c 300

# HLS 静态资源（Vite public）
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:5173/hls/test.m3u8
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:5173/hls/seg_000.ts

# WebSocket（浏览器 DevTools → Network → WS，或 node/websocat）
# ws://localhost:5173/ws 应收到 hello，随后 event / track_point / stats 帧
```

无头 Chrome 手动验收（本机有 Chrome 时）：

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless=new --disable-gpu --window-size=1600,900 \
  --virtual-time-budget=10000 --screenshot=/tmp/p1-web.png http://127.0.0.1:5173/
```

## 已知限制

- HLS 演示流为本地 VOD 测试片源（`test.m3u8`），非真实 RTSP/GB28181；真实接入属 P1 现场阶段。
- 生产构建中 three.js 主包约 698 kB（gzip ≈ 188 kB），hls.js 为按需加载的独立分包（gzip ≈ 181 kB），仅在有视频流时加载。
- 轨迹为前端内存态：保留 10 分钟、单轨 300 点、场景最多 40 条；刷新页面后重新从 API 拉取。
- P1 不做权限/多租户/真实 AI 指标；标定 UI 以「粘贴控制点 JSON」为最小可用形态，现场标定工具留 P1 后续阶段。
