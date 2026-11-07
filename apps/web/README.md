# 数字孪生安防平台 · Web 前端（P0）

Vite + Vue 3 + TypeScript + Three.js 实现的 P0 三维监控客户端：400 m × 400 m 场景、相机视锥、实时事件上图、事件确认闭环。

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

要求 Node.js `^20.19` 或 `>=22.12`（Vite 8 要求），pnpm 10。

## 环境变量

在 `apps/web` 下创建 `.env.local`（不入库）或直接使用 shell 环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE` | 空（同源） | 后端源站，如 `http://127.0.0.1:8080`。为空时前端请求同源 `/api`、`/ws`，由 Vite 代理到 `127.0.0.1:8080`；设置后前端直接请求该源站，Vite 代理目标也改为该地址。仅写协议 + 主机 + 端口，不要带路径。 |
| `VITE_SITE_NAME` | `数字孪生安防平台` | 顶栏站点标题。 |

默认配置无需任何环境变量即可与本地 Go API（`http://127.0.0.1:8080`）联调。

## 后端契约

严格遵循 `docs/API.md`，不改动接口。前端使用：

- `GET /api/cameras`：相机台账（位置/朝向/FOV/量程/状态）。
- `GET /api/events?limit=200`：事件列表（按 `occurred_at` 倒序）。
- `POST /api/events/{id}/ack`：确认事件，更新列表状态。
- `WS /ws`：消费 `hello` / `event` / `camera_status` / `stats`；客户端不发送消息，断线指数退避重连（1 s → 2 s → … → 30 s 封顶，连接成功后重置）。
- 启动时若 REST 失败：显示离线横幅、每 10 s 自动重试；WS 连通（收到 `hello`）后也会补拉数据。

## 页面与交互

- **顶栏**：站点标题、在线相机数 / 相机总数、事件总数（优先 WS `stats`）、FPS、WebSocket 状态灯、本地时钟。
- **三维场景**（`src/scene/TwinScene.vue`）：
  - 400 × 400 m 地面网格、两栋建筑（A 栋 ENU 中心 `-40,40`、`60×40×20 m`；B 栋 `60,-20`、`50×30×14 m`）、渐变天穹 + 雾、环境光 + 主平行光（阴影）+ 冷色补光。
  - 6 台相机标记：发光球体本体 + 半透明视锥（按 `heading`、`fov_h`、`range_m` 生成，颜色随状态：在线绿 / 离线红 / 异常琥珀），悬停显示编号、名称、状态与参数，点击选中并高亮，右侧展示详情。
  - 事件标记：事件世界坐标处的扩散脉冲环 + 竖直光柱 + 顶部标记体，按 `severity` 着色（提示青 / 警告琥珀 / 严重红），约 **60 s 后自动淡出**（事件流仍保留历史）；点击标记打开对应事件（自动滚动并高亮列表条目，必要时重置筛选）。
  - OrbitControls 旋转/平移/缩放，左下角 FPS 与操作提示，右下角相机/事件颜色图例。
- **右侧面板**：
  - 相机详情：状态、区域、ENU 坐标、方位角、水平/垂直视场、量程、更新时间，「定位视角」播放运镜。
  - 实时事件流：类型 / 等级筛选、相对时间、置信度、确认按钮（`确认` → `已确认`，失败提示可关闭），点击条目定位所属相机。
- **断网/后端未启动**：场景照常可浏览（地面/建筑/轨道控制），顶部显示明确横幅说明目标地址与错误，并支持手动重试；相机与事件区域显示空状态。

## 坐标与渲染约定

- 数据坐标：局部 ENU 米制（x 东、y 北、z 上），见 `docs/API.md`。
- Three.js 映射：`three.x = ENU.x`，`three.y = ENU.z`，`three.z = -ENU.y`（`src/scene/coordinates.ts`）。
- `heading`：正北 0°、顺时针；映射到 Three.js 水平方向为 `(sin h, 0, -cos h)`。
- 视锥：`ConeGeometry` 平移使顶点位于相机位置，局部 +Z 对准 heading；锥长取 `range_m`（夹取 18–90 m 以保证可读性），半径按 `fov_h` 计算。

## 目录结构

```
apps/web/
├─ index.html
├─ vite.config.ts            # 端口 5173；/api、/ws 代理；VITE_API_BASE 覆盖
├─ src/
│  ├─ main.ts / App.vue      # 应用入口与三栏布局
│  ├─ types.ts               # 与 docs/API.md 对齐的类型 + WS 消息类型
│  ├─ labels.ts              # 中文标签、颜色、时间格式化
│  ├─ api.ts                 # REST 客户端与错误类型
│  ├─ ws.ts                  # WS 客户端（消息校验 + 指数退避重连）
│  ├─ store/twin.ts          # 轻量响应式 store（未引入 Pinia）
│  ├─ components/            # TopBar / CameraDetail / EventFeed / OfflineBanner
│  ├─ scene/                 # TwinScene + 环境/相机标记/事件标记/坐标/资源释放
│  └─ styles/main.css        # 深蓝指挥中心风格（1280–1920 适配）
└─ README.md
```

## 哪些是 Mock，哪些是实时数据

| 内容 | 来源 |
| --- | --- |
| 地面网格、两栋建筑、天空/雾、灯光 | **前端静态 Mock**（P0 固定布局，尺寸见上；P1 接真实模型/标定） |
| 相机台账（状态、坐标、朝向、FOV、量程） | **实时**：`GET /api/cameras` + WS `camera_status` |
| 事件列表与三维事件标记 | **实时**：`GET /api/events` + WS `event`；确认走 `POST .../ack` |
| 在线相机数 / 事件总数 / WS 客户端数 | **实时**：WS `stats`（5 s 一次），启动时用 REST 兜底 |
| 视频流 | **未接入**（P0 范围外；`stream_url` 仅台账字段，不播放） |
| AI 推理 | **未接入**，由 `services/simulator` 按 `/api/ingest/detections` 契约模拟 |

## 验证方式

```bash
# 1. 构建
pnpm install && pnpm build

# 2. 启动（API 已运行在 8080 时）
pnpm dev

# 3. 代理连通性
curl -s http://127.0.0.1:5173/api/health
curl -s "http://127.0.0.1:5173/api/cameras"
curl -s http://127.0.0.1:5173/ | grep -q 'id="app"' && echo "app shell ok"
```

WS 代理可用 `websocat ws://127.0.0.1:5173/ws` 或浏览器 DevTools → Network → WS 检查 `hello/stats` 帧。

## 已知限制

- 生产包中 three.js 占主要体积（min 约 666 kB / gzip 约 178 kB），首屏目标按 P0「≤3 s」在局域网/本机可达；如需进一步优化可对 three 做 vendor 拆包。
- 无浏览器自动化环境，交互与视觉效果需人工在浏览器确认；构建/类型检查/代理链路已通过命令行验证。
- P0 不做权限、多租户、视频播放、真实标定；这些留在 P1/P2。
