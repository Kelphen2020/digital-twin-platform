# 数字孪生安防平台 · Web 前端（P2.0）

Vite + Vue 3 + TypeScript + Three.js 实现的三维安防平台客户端：登录 / RBAC、三维场景、HLS 视频、实时轨迹、告警处置、工单流转、统计看板、相机规则与单应标定。契约见 `docs/API.md`（P0）、`docs/API_P1.md`（P1）、`docs/API_P2.md`（P2，均为固定契约）。

## 快速开始

```bash
cd digital-twin-platform/apps/web
pnpm install
pnpm dev          # http://127.0.0.1:5173
```

打开页面后使用演示账号登录（P2.0 默认后端 `AUTH_MODE=on`）：

| 用户名 | 密码 | 角色 | 权限 |
| --- | --- | --- | --- |
| `admin` | `Admin@123456` | 管理员 | 全部：规则配置、标定、任意相机修改 |
| `operator` | `Operator@123` | 值班员 | 事件处置、工单创建/流转、区域（`area_gate`、`area_parking`）内相机修改 |
| `viewer` | `Viewer@123` | 观察员 | 只读：相机/事件/轨迹/统计/工单/规则 |

其他脚本：

| 命令 | 说明 |
| --- | --- |
| `pnpm dev` | 开发服务器，固定端口 5173（`strictPort`） |
| `pnpm build` | 类型检查（`vue-tsc --noEmit`）+ 生产构建到 `dist/` |
| `pnpm preview` | 预览构建产物，端口 4173（同样代理 `/api`、`/ws`） |
| `pnpm typecheck` | 仅类型检查 |

要求 Node.js `^20.19` 或 `>=22.12`（Vite 8 要求），pnpm 10。运行时依赖：`vue`、`three`、`hls.js`（按需动态加载）。

## 环境变量

在 `apps/web` 下创建 `.env.local`（不入库）或使用 shell 环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE` | 空（同源） | 后端源站，如 `http://127.0.0.1:8080`。为空时前端请求同源 `/api`、`/ws`，由 Vite 代理到 `127.0.0.1:8080`；设置后前端直接请求该源站，Vite 代理目标也改为该地址。仅写协议 + 主机 + 端口。 |
| `VITE_SITE_NAME` | `数字孪生安防平台` | 顶栏站点标题。 |
| `VITE_API_TOKEN` | 空 | **机器/联调兜底**静态令牌（P1 遗留）。正常浏览请用登录页获取会话令牌；该变量仅在未登录时作为 Bearer/WS token 兜底，不影响登录页与角色 UI。 |

## 功能总览

### 登录与会话（P2）

- 登录页展示演示账号，点击自动填入；`POST /api/auth/login` 成功后令牌持久化到 `localStorage`，刷新页面通过 `GET /api/auth/me` 恢复会话。
- 所有 REST 自动携带 `Authorization: Bearer <token>`，WebSocket 自动追加 `?token=`。
- 任意已认证请求返回 `401` → 自动登出并回到登录页，提示「登录已过期，请重新登录」。
- 顶栏用户区展示显示名、角色、负责区域，支持退出登录；退出后清空上一会话的相机/事件/轨迹/工单数据。

### 角色权限（P2，后端为最终裁决）

| 能力 | viewer | operator | admin |
| --- | --- | --- | --- |
| 查看相机/事件/轨迹/统计/工单/规则 | ✅ | ✅ | ✅ |
| 事件确认 / 关闭 / 派单 | ❌（按钮禁用 + 只读提示） | ✅ | ✅ |
| 工单创建 / 状态、负责人、备注流转 | ❌（只读） | ✅ | ✅ |
| 相机状态/坐标/朝向修改 | ❌ | ✅ 仅负责区域内（区域外后端返回 `403 out_of_scope`，前端浮层提示） | ✅ 任意相机 |
| 相机规则配置 | ❌（只读） | ❌（只读） | ✅ |
| 相机标定 | ❌ | ❌ | ✅ |

权限错误统一以顶部 Toast 提示（`403 out_of_scope` / 无权限 / 登录过期）。

### 三维场景与视频（P0/P1）

- 400 × 400 m 地面网格；两栋建筑（A 栋 ENU 中心 `-40,40`、`60×40×20 m`；B 栋 `60,-20`、`50×30×14 m`）；渐变天穹 + 雾 + 环境光/平行光（阴影）。
- 相机标记（球体 + 按 `heading`/`fov_h`/`range_m` 的视锥，颜色随状态）、事件脉冲标记（约 60 s 淡出）、轨迹折线（按 class 着色、旧点淡出、显示开关）。
- 相机详情内 HLS 播放 `stream_url`（演示流 `/hls/test.m3u8`，Vite 托管 `public/hls/`）；原生 HLS 或按需加载 hls.js；LIVE 徽标、播放/暂停、加载/错误重试、切换相机自动清理；无视频源显示占位。

### 事件处置（P1/P2）

- 点击事件流或 3D 标记打开处置面板：事件信息、状态、动作时间线（`GET /api/events/{id}`）。
- `确认` / `关闭事件（备注）` / `派单（目标 + 备注）`，监听 WS `event_updated` 实时刷新；事件流可按类型/等级筛选、导出 CSV（`/api/stats/events.csv`）。

### 工单（P2）

- 右侧「工单」页签：列表、状态筛选、详情展开；状态机 `open → assigned → processing → done / cancelled`，支持修改状态、负责人、备注，展示 `closed_at`。
- 事件处置面板「创建工单」：`POST /api/orders`，可填写优先级、负责人、备注；同一事件允许重复建单（P2 契约）。
- 实时接收 WS `order_updated` 刷新列表。

### 统计看板（P1）

- `GET /api/stats/summary`：事件总数、按严重程度/类型分布、高发相机 TOP、按小时柱状图；近 1 小时 / 近 24 小时切换、打开自动加载、30 s 自动刷新、CSV 导出。

### 相机规则（P2，admin 可编辑）

- 相机详情「规则配置/查看规则」弹窗：`GET/PUT /api/cameras/{id}/rules`。
- 可编辑置信度阈值（0–1）、去重窗口（0–300 s）、启用类别勾选、ROI 多边形（归一化 0–1 坐标，支持增删点/4 点默认/全画面）；显示 `updated_at`。
- viewer/operator 为只读视图（输入禁用、无保存按钮）。

### 相机标定（P1，admin）

- 相机详情显示「已标定（RMSE/点数/时间）/ 未标定」；admin 可打开标定弹窗：粘贴 4–20 组控制点 JSON → `POST /api/calibration/solve` 展示 3×3 单应矩阵与残差 → `PUT /api/cameras/{id}/calibration` 保存，支持删除。

## 后端契约（前端实际调用）

| 方法 | 路径 | 角色 | 用途 |
| --- | --- | --- | --- |
| POST | `/api/auth/login` | 公开 | 登录 |
| GET | `/api/auth/me` | 已登录 | 恢复/校验会话 |
| GET | `/api/cameras` | 全部 | 相机台账（含 `stream_url`、`calibration`） |
| PATCH | `/api/cameras/{id}` | operator/admin | 状态/坐标/朝向（区域校验） |
| GET/PUT | `/api/cameras/{id}/rules` | 全部 / admin | 相机规则 |
| PUT/DELETE | `/api/cameras/{id}/calibration` | admin | 保存/删除标定 |
| POST | `/api/calibration/solve` | admin | 单应矩阵求解 |
| GET | `/api/events?limit=200` | 全部 | 事件列表 |
| GET | `/api/events/{id}` | 全部 | 事件详情 + 动作时间线 |
| POST | `/api/events/{id}/ack|close|dispatch` | operator/admin | 事件处置 |
| GET | `/api/tracks?camera_id=&limit=500` | 全部 | 历史轨迹 |
| GET | `/api/stats/summary`、`/api/stats/events.csv` | 全部 | 统计 / 导出 |
| GET/POST | `/api/orders`、`GET/PATCH /api/orders/{id}` | 全部 / operator/admin | 工单 |
| WS | `/ws?token=` | 已登录 | `hello/event/event_updated/camera_status/track_point/order_updated/stats` |

## 坐标与渲染约定

- 数据坐标：局部 ENU 米制（x 东、y 北、z 上）；Three.js 映射 `three.x = ENU.x`、`three.y = ENU.z`、`three.z = -ENU.y`。
- `heading`：正北 0°、顺时针；Three.js 水平方向 `(sin h, 0, -cos h)`；视锥锥长取 `range_m`（夹取 18–90 m）。
- 轨迹绘制于地面 0.4 m 以上；3 分钟窗口外接近背景色，单轨最多 300 点 / 场景最多 40 条。

## 目录结构

```
apps/web/
├─ index.html / vite.config.ts / tsconfig.json
├─ public/hls/                   # 演示 HLS（由 scripts/make_hls.sh 生成）
├─ public/favicon.svg
└─ src/
   ├─ main.ts / App.vue          # 登录门禁 + 三栏布局（事件流/统计/工单页签）
   ├─ types.ts / labels.ts       # P0–P2 数据模型、中文标签、角色/工单/轨迹颜色
   ├─ api.ts                     # REST 客户端（Bearer、401 处理、P2 端点、错误文案）
   ├─ auth/token.ts              # 会话令牌集中读写（避免循环依赖）
   ├─ ws.ts                      # WS 客户端（token、消息校验、指数退避）
   ├─ store/
   │  ├─ auth.ts                 # 登录/会话/角色权限矩阵/区域判定
   │  ├─ twin.ts                 # 相机/事件/轨迹/统计/工单/选择状态
   │  └─ ui.ts                   # Toast 队列
   ├─ components/
   │  ├─ LoginView.vue / ToastHost.vue / TopBar.vue / OfflineBanner.vue
   │  ├─ CameraDetail.vue / CameraVideo.vue / CalibrationDialog.vue / CameraRulesPanel.vue
   │  ├─ EventFeed.vue / EventDetail.vue / StatsPanel.vue / OrdersPanel.vue
   │  └─ （其余见代码）
   ├─ scene/                     # TwinScene + 环境/相机标记/事件标记/轨迹/坐标/资源释放
   └─ styles/main.css            # 深蓝指挥中心风格（1280–1920）
```

## 哪些是 Mock，哪些是实时数据

| 内容 | 来源 |
| --- | --- |
| 地面网格、两栋建筑、天空/雾、灯光 | **前端静态 Mock**（P0 固定布局） |
| HLS 测试视频 | **本地演示流**（ffmpeg 生成到 `public/hls/`，UI 固定 LIVE 徽标）；接真实媒体服务时替换 `stream_url` |
| 用户/角色/会话 | **实时**：`/api/auth/login` + `/api/auth/me`；P2 演示账号为预置数据，生产必须替换 |
| 相机台账、状态、视频地址、标定、规则 | **实时**：REST + WS `camera_status` |
| 事件、处置状态与审计 | **实时**：REST + WS `event`/`event_updated` |
| 轨迹 | **实时**：`/api/tracks` + WS `track_point`（模拟器产生） |
| 工单 | **实时**：`/api/orders` + WS `order_updated` |
| 统计与 CSV | **实时**：`/api/stats/summary`、`/api/stats/events.csv` |
| AI 推理 | **未接入**，由 `services/simulator` 按 `/api/ingest/detections` 契约模拟 |

## 验证方式

```bash
cd digital-twin-platform/apps/web
pnpm install && pnpm typecheck && pnpm build

# 后端 AUTH_MODE=on 时：登录拿 token
TOKEN=$(curl -s -X POST http://localhost:5173/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@123456"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')

curl -s http://127.0.0.1:5173/ | grep -q 'id="app"' && echo "app shell ok"
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/cameras | head -c 200
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/cameras/cam_001/rules"
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/orders?limit=10"

# 未认证访问应为 401
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5173/api/cameras
```

无头 Chrome 手动验收（本机有 Chrome 时）：

```bash
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless=new --disable-gpu --window-size=1600,900 \
  --virtual-time-budget=12000 --screenshot=/tmp/p2-web.png http://127.0.0.1:5173/
```

## 已知限制

- 会话令牌存 `localStorage`（P2 演示形态），无刷新令牌/多标签同步；生产需接入统一认证与退出失效。
- P2 演示账号（admin/operator/viewer）为预置数据，上线前必须替换；`AUTH_MODE=off` 仅用于本机联调，前端仍要求登录页。
- 相机规则 ROI 为归一化坐标的简单多边形编辑器（增删点/默认 4 点），未做拖拽手柄；适合演示与最小可用配置。
- 工单仅支持从事件创建（契约），未做分页（一次拉取 100 条后前端过滤）。
- 生产构建中 three.js 主包约 726 kB（gzip ≈ 196 kB），hls.js 为按需加载独立分包（gzip ≈ 181 kB）。
- 真机 GPU 帧率与画布交互（拖拽/悬停/3D 点选）未自动化；headless 软渲染约 45 FPS 不代表真机（目标 ≥30 fps@1080p）。
- P2 不做多租户/权限管理后台/审批流；这些留在后续阶段。
