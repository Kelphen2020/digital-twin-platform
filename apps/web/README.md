# 数字孪生安防平台 · Web 前端（P9.0）

Vite + Vue 3 + TypeScript + Three.js 实现的三维安防平台客户端：登录 / RBAC、三维场景、HLS 视频、实时轨迹、告警处置、工单流转、统计看板、相机规则与单应标定。契约见 `docs/API.md`（P0）、`docs/API_P1.md`（P1）、`docs/API_P2.md`（P2）、`docs/API_P3.md`（P3）、`docs/API_P4.md`（P4）、`docs/API_P5.md`（P5）、`docs/API_P6.md`（P6）、`docs/API_P7.md`（P7）、`docs/API_P8.md`（P8）、`docs/API_P9.md`（P9，均为固定契约）。

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
| 平台设置（通知/升级/自动派单）与审计/通知投递 | ❌ | ❌ | ✅ |
| 值班组 | 只读（值班 tab） | 只读（值班 tab） | ✅ 新建/编辑/删除 |
| 事件总线状态 / 回放 | ❌（tab 隐藏） | ❌（tab 隐藏） | ✅（总线 tab） |
| 用户与账号管理 | ❌（tab 隐藏，API 403） | ❌（tab 隐藏，API 403） | ✅（用户 tab） |

权限错误统一以顶部 Toast 提示（`403 out_of_scope` / 无权限 / 登录过期）。

**数据级权限（P3）**：非 admin 且 `areas` 非空时，后端对相机/事件/轨迹/工单/统计只返回负责区域内的数据；前端侧栏顶部显示「仅显示负责区域：…」提示。单相机/事件/工单详情越权返回 `403 out_of_scope`，前端以 Toast 提示而不影响其他功能。

### 三维场景与视频（P0/P1）

- 400 × 400 m 地面网格；两栋建筑（A 栋 ENU 中心 `-40,40`、`60×40×20 m`；B 栋 `60,-20`、`50×30×14 m`）；渐变天穹 + 雾 + 环境光/平行光（阴影）。
- 相机标记（球体 + 按 `heading`/`fov_h`/`range_m` 的视锥，颜色随状态）、事件脉冲标记（约 60 s 淡出）、轨迹折线（按 class 着色、旧点淡出、显示开关）。
- 相机详情内 HLS 播放 `stream_url`（演示流 `/hls/test.m3u8`，Vite 托管 `public/hls/`）；原生 HLS 或按需加载 hls.js；LIVE 徽标、播放/暂停、加载/错误重试、切换相机自动清理；无视频源显示占位。

### 事件处置（P1/P2）

- 点击事件流或 3D 标记打开处置面板：事件信息、状态、动作时间线（`GET /api/events/{id}`）。
- 超时升级显示「已升级」徽标（`escalated_at`）；升级后超时仍关闭则显示「再升级」徽标（`re_escalated_at`）与再升级时间，WS 推送时 Toast「告警再次升级：…」。
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

### 告警升级（P3）

- 事件新增 `escalated_at`：事件流与处置详情显示红色「已升级」徽标（呼吸高亮），详情展示升级时间。
- 服务端按升级设置每 15 s 扫描超时未处置事件；升级时 WS 推送 `event_updated`，前端显示「告警已升级」Toast；升级同时触发一次通知投递。

### 平台设置（P3，admin）

- 右侧「设置」页签（仅 admin 可见）：
  - **通知（Webhook）**：启用开关、Webhook URL、格式（`generic` / `feishu`）、最低严重级别、超时（1–30 s），保存后经 `GET /api/settings/notifications` 确认持久化。
  - **告警升级**：启用开关、超时未处置时限（0–86400 s）、适用严重级别勾选，保存经 `GET /api/settings/escalation` 确认。
- 前端做与契约一致的本地校验（URL 为 http/https、数值范围等），保存成功/失败均有 Toast。

### 审计与通知投递（P3，admin）

- 右侧「审计」页签（仅 admin 可见），子页签：
  - **操作审计**：`GET /api/audit?limit=100`，展示 actor / action / resource / 时间 / detail 摘要（含 `event.escalate`、`settings.*.put` 等）。
  - **通知投递**：`GET /api/notifications?limit=50`，展示 kind / event_id / 格式 / 成功或失败 / `status_code` / 错误 / url。
- 刷新按钮同时拉取两类数据；操作审计与通知投递均有空态与错误重试。

### 值班组与自动派单（P4）

- 所有登录用户可在右侧「值班」页签查看：
  - **当前值班**：按所选相机区域解析（`GET /api/duty/current?area_id=`），展示值班组、当前班次（名称/起止时间）与当班人员（当前用户高亮）；无班次时按值班人员全天值班。
  - **值班组列表**：名称、负责区域、值班人员、rotation、班次摘要、启用状态与更新时间；operator/viewer 只读。
- admin 可在同一页签内新建/编辑/删除值班组（名称、区域多选、值班人员勾选、rotation=班组名/班次成员轮转、启用开关），并维护**班次编辑器**（每行：名称、HH:MM 起止、周一–周日多选、班次成员多选、添加/删除）；删除为两步确认；保存/删除后刷新当前值班解析。
- 值班解析规则：优先匹配包含相机区域且启用的值班组，其次使用 `areas=[]` 的全局兜底组；配置班次时只有落在班次内（按服务端 Asia/Shanghai 时间、ISO 周几）才算值班，无人班次 → 无值班。

### 值班排班与轮转 / 超时再升级（P8）

- **值班班次**：值班组支持 `rotation`（`group` 派单给班组名 / `operator` 按班次成员轮转）与 `shifts[]`（名称、`HH:MM` 起止、ISO 周几 1–7、班次成员）；`end <= start` 视为跨夜；新班次由服务端生成 id，编辑时保留已有 id。前端在「值班」页签提供班次行编辑（添加/删除/周几/成员），并做名称、HH:MM、周几范围校验。
- **当前值班**：`GET /api/duty/current?area_id=` 响应新增 `shift`；卡片显示当前班次与当班人员，非值班时间/无班次给出明确提示。
- **工单去重与轮转**：自动派单对同一事件已有未关闭工单时跳过；`rotation=operator` 时 assignee 为轮转到的操作员用户名，工单 note 原样包含班组/班次信息（工单列表展示负责人与 note）。
- **超时再升级**：升级设置新增 `second_after_seconds`（0–604800，0=关闭）；升级后超过该时长仍未关闭 → 写入 `re_escalated_at`、WS `event_updated`、通知（kind=`escalation`）、审计 `event.re_escalate` 并触发再升级自动派单（受工单去重限制）。事件流/详情显示「再升级」徽标与时间，WS 时 Toast「告警再次升级：…」。

### 连接质量与限流反馈（P9）

- **顶栏连接质量指示器**（WS 状态点旁）：显示最近 **5 秒** WS 帧速率（帧/s）、自页面加载以来的**重连次数**（`↻N`），并以颜色表示健康度——绿色=已连接且最近 5s 有帧/新连接，琥珀色=重连中或超过 5s 无帧，红色=断开等待重连。悬停 tooltip 展示帧总数、5s 速率、重连次数、连接时长、最近帧年龄。
- **统计口径**：仅计入 WebSocket 文本帧；断线指数退避重连成功后重连计数 +1；登出/销毁时重置统计。断线/重连失败不影响 P0–P8 功能（REST 轮询与离线横幅照常）。
- **429 限流反馈**：`api.ts` 将 HTTP **429**（P9 ingest 限流 `{"error":"rate_limited"}`）统一映射为中文 Toast「请求过于频繁，已限流」；`INGEST_RATE_LIMIT` 默认 0（关闭），默认行为不变。

### 通知可靠性与审计（P4）

- 通知设置新增：`max_attempts`（1–5，含首次）、`backoff_seconds`（指数退避基数，≥0）、`silence_seconds`（同相机+类型+严重级别静默窗口 0–3600s）。
- 「审计 → 通知投递」展示每条记录的 `attempts`（实际尝试次数，如“尝试 2 次”）。
- 相关指标由后端 `/metrics` 暴露（`dsh_twin_notifications_*`、`dsh_twin_escalations_total`、`dsh_twin_orders_total`、`dsh_twin_duty_dispatches_total`、`dsh_twin_duty_skipped_total`）。

### 事件总线与回放（P5）

- 右侧「总线」页签（仅 admin）：
  - **状态卡片**：驱动程序、存储路径、已发布 / 已回放条数、缓冲起点 offset、下一条 offset、订阅者数、最近发布时间；支持刷新。
  - **回放表单**：`from_offset`（≥0）、`to_offset`（可选，留空到当前尾部）、`limit`（1–5000，默认 500）、`notify`（是否触发 Webhook，默认关闭）；提交调用 `POST /api/bus/replay`，成功 Toast 显示回放条数，随后自动刷新状态。
  - 异常处理：后端 `400 bad_request`（区间非法）与 `409 bus_driver_unsupported`（驱动/缓冲不支持）以 Toast 提示；同时保留客户端基础校验（区间、limit）。
- **回放事件流**：WS `event_replay` 帧（`data: {event, offset, replay}`）追加到事件流顶部回放区，显示紫色「回放 · offset N」徽标与「回放」计数；回放条目不计入事件总数、不生成实时告警与三维标记、不提供确认按钮（仅「历史」标记），可点击打开事件详情。`event_escalated` 回放仍走 `event_updated` 正常更新。
- 回放 feed 最多保留 100 条，实时事件原有 200 条上限不变。

### 三维增强：2.5D 底图 / LOD / glTF / 视频融合（P6）

- **站点配置**：登录后 `GET /api/site` 获取 `extent`（地面/网格尺寸）、`models`（glTF 清单与 transform）、`map`；接口失败/403/404 时使用内置兜底站点（400×400 m），场景不回退加载。
- **2.5D 矢量底图**（默认开启，顶栏「2.5D底图」切换）：程序化 CanvasTexture（2048²）按站点 extent 绘制——区域/建筑轮廓（优先 `GET /api/areas` 的 building polygon，缺失时使用固定建筑布局）、程序化道路、相机点位与 code、指北针、50 m 比例尺、扫描线与暗角；关闭后仅保留三维网格。
- **建筑 LOD**（顶栏「模型细节」切换）：`THREE.LOD` 近景 = 楼体 + 屋顶 + 楼层线 + 轮廓，中景 = 简化盒体，远景 = 地面轮廓；glTF 加载成功时 glTF 作为近景层级（中/远景仍用程序化盒体/轮廓），关闭模型细节则使用简化盒体。
- **glTF 加载**：`GLTFLoader` 按需动态加载 `site.models`（支持 gltf/glb），应用 transform（ENU 位置、`rotation_y` 角度、缩放）；加载中/成功/部分失败/回退在场景左下角「模型：…」指示；失败或无模型时保留程序化建筑（回退指示）。
- **视频纹理融合**（默认关闭，顶栏「视频融合」切换）：为选中相机创建**独立**隐藏 `<video>` + hls.js 实例播放 `stream_url`，把 `VideoTexture` 贴到视锥内量程 60% 处的平面（尺寸按 `fov_h`/`fov_v` 计算），带边框、暗角与 LIVE 标签；相机切换/关闭开关时销毁播放器与纹理，不影响右侧相机详情里的 HLS 播放器。
- **工具栏**：顶栏紧凑工具栏含 轨迹 / 视频融合 / 模型细节 / 2.5D底图 四个开关（会话内状态，刷新恢复默认）；7 个侧栏 tab 保持不变。

### 用户与账号管理（P7，admin）

- 右侧「用户」页签（第 8 个 tab，仅 admin）：`GET /api/users` 列表展示用户名、显示名、角色、负责区域、启用状态与创建/更新时间；「当前用户」「已停用」「启用中」徽标。
- **新建用户**弹窗：用户名（3–32 字符）、密码（≥8）、显示名、角色（viewer/operator/admin）、负责区域多选（`GET /api/areas`）、启用开关；重复用户名 `409 username_taken` 与 400 校验错误以 Toast 提示。
- **编辑用户**弹窗：显示名/角色/区域/启用状态 + 可选新密码（留空不修改）；编辑自己时禁用角色与启用开关并提示 self_protected，保存按钮同时禁用；若仍触发后端保护（400 self_protected）以 Toast 提示。
- **删除用户**：两步确认（删除 → 确认删除）；当前登录账号删除按钮禁用（self_protected 保护），后端同样会拒绝。
- 角色/区域/启用状态变更在**下一次登录**生效（JWT 内容不变）；停用账号无法登录（`401 invalid_credentials`），已有会话的 401 自动登出逻辑沿用 P2。
- 审计新增 `user.create` / `user.update` / `user.delete`（actor=当前管理员）。

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
| GET/PUT | `/api/settings/notifications` | admin | 通知（Webhook）设置 |
| GET/PUT | `/api/settings/escalation` | admin | 告警升级设置 |
| GET | `/api/audit?limit=100` | admin | 操作审计 |
| GET | `/api/notifications?limit=50` | admin | 通知投递记录（含 `attempts`） |
| GET | `/api/areas` | 已登录 | 区域列表（值班组区域多选） |
| GET | `/api/duty/groups` | 已登录 | 值班组列表 |
| POST/PATCH/DELETE | `/api/duty/groups`、`/api/duty/groups/{id}` | admin | 值班组增改删（P8 含 rotation/shifts） |
| GET | `/api/duty/current?area_id=&at=` | 已登录 | 区域当前值班组 + 当前班次（P8） |
| GET | `/api/site` | 已登录 | 站点范围 / glTF 模型清单 / 底图配置 |
| GET/POST | `/api/users` | admin | 用户列表 / 新建用户 |
| PATCH/DELETE | `/api/users/{id}` | admin | 修改 / 删除用户（self_protected 保护） |
| GET | `/api/bus/status` | admin | 事件总线运行状态 |
| POST | `/api/bus/replay` | admin | 按 offset 区间回放事件 |
| WS | `/ws?token=` | 已登录 | `hello/event/event_updated/event_replay/camera_status/track_point/order_updated/stats` |

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
   │  ├─ twin.ts                 # 相机/事件/轨迹/统计/工单/选择状态（含升级 Toast）
   │  ├─ admin.ts                # P3 通知/升级设置、审计、通知投递、P5 总线状态/回放、P7 用户管理
   │  ├─ wsQuality.ts            # P9 WS 帧速率/重连次数/健康度统计
   │  ├─ duty.ts                 # P4 值班组 / 区域当前值班解析
   │  └─ ui.ts                   # Toast 队列
   ├─ components/
   │  ├─ LoginView.vue / ToastHost.vue / TopBar.vue / OfflineBanner.vue
   │  ├─ CameraDetail.vue / CameraVideo.vue / CalibrationDialog.vue / CameraRulesPanel.vue
   │  ├─ EventFeed.vue / EventDetail.vue / StatsPanel.vue / OrdersPanel.vue
   │  ├─ SettingsPanel.vue       # P3 通知/升级设置（admin）
   │  ├─ AuditPanel.vue          # P3 操作审计 + 通知投递（admin，P4 显示 attempts）
   │  ├─ DutyPanel.vue           # P4/P8 值班组+班次+轮转（admin 增改删；其他角色只读）
   │  ├─ BusPanel.vue            # P5 事件总线状态 / 回放（admin）
   │  ├─ UserPanel.vue           # P7 用户与账号管理（admin）
   │  └─ （其余见代码）
   ├─ scene/                     # TwinScene + 环境/相机标记/事件标记/轨迹/坐标/资源释放
   │  ├─ buildings.ts            # 建筑 LOD（近细节/中盒体/远轮廓）与 glTF 近景接线
   │  ├─ baseMap.ts              # 2.5D 程序化矢量底图 CanvasTexture
   │  ├─ gltf.ts                 # GLTFLoader 站点模型加载与 transform
   │  ├─ videoFusion.ts          # 选中相机 HLS VideoTexture 视锥内视频面
   │  └─ siteDefaults.ts         # /api/site 兜底站点
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
| 统计与 CSV | **实时**：`/api/stats/summary`、`/api/stats/events.csv`（按角色区域过滤） |
| 告警升级 | **实时**：服务端升级 worker + WS `event_updated`（`escalated_at`） |
| 通知设置 / 投递记录 | **实时**：`/api/settings/notifications`、`/api/notifications`；投递需要可达的 Webhook 接收端 |
| 升级设置 | **实时**：`/api/settings/escalation` |
| 操作审计 | **实时**：`/api/audit`（含 `duty.*`、`order.auto_dispatch*`） |
| 值班组 / 自动派单 | **实时**：`/api/duty/*` + 升级设置 `auto_dispatch` + WS `order_updated` |
| 事件总线状态 / 回放 | **实时**：`/api/bus/status` + `POST /api/bus/replay` + WS `event_replay`（当前后端驱动为 `jsonl`，路径 `data/bus/events.jsonl`） |
| 用户与账号 | **实时**：`/api/users` CRUD（密码不返回；角色/区域下次登录生效；停用即禁止登录） |
| WS 连接质量 / 限流 | **实时**：WS 帧计数（前端统计）、REST 429 `rate_limited` → 中文 Toast；`INGEST_RATE_LIMIT` 默认 0 |
| 站点范围 / 底图配置 | **实时**：`GET /api/site`（extent/models/map） |
| glTF 站点模型 | **静态资源**：`public/models/site.gltf`（由后端 `/api/site` 清单引用；当前为 1 号楼/2 号楼/东门门架） |
| 2.5D 底图 / LOD / 视频融合 | **前端程序化实现**：Canvas 矢量底图、THREE.LOD、VideoTexture（视频源为相机 `stream_url`） |
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

# P3：管理员设置 / 审计 / 通知投递
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/settings/escalation
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/settings/notifications
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/audit?limit=20"
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/notifications?limit=20"

# P3：非 admin 访问管理端点应为 403 forbidden；operator 相机列表仅返回负责区域
OP_TOKEN=$(curl -s -X POST http://localhost:5173/api/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"Operator@123"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $OP_TOKEN" http://localhost:5173/api/settings/escalation
curl -s -H "Authorization: Bearer $OP_TOKEN" http://localhost:5173/api/cameras | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["items"]))'

# P4：值班组与自动派单
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/areas
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/duty/groups
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/duty/current?area_id=area_gate"
# admin 创建值班组（operator 只读）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"东门值班组","areas":["area_gate"],"operators":["operator"],"active":true}' \
  http://localhost:5173/api/duty/groups
# 通知可靠性字段
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/settings/notifications
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/settings/escalation

# P9：WS 指标（后端） / 限流（默认关闭）
curl -s http://localhost:8080/metrics | grep -E 'dsh_twin_ws_(frames|dropped)_total|dsh_twin_ingest_rejected_total'
# 顶栏 WS 点旁可见「N.N/s ↻M」；悬停显示帧总数/连接时长/最近帧年龄；断线重连后 ↻ 递增
# 若设置 INGEST_RATE_LIMIT>0，超限 ingest 返回 429 {error:"rate_limited"}，前端 Toast「请求过于频繁，已限流」

# P8：值班班次 + 轮转（admin 创建含班次的值班组；GET current 可见 shift）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"P8 测试值班组","areas":["area_gate"],"operators":["operator"],"active":true,"rotation":"operator","shifts":[{"name":"早班","start":"00:00","end":"23:59","weekdays":[1,2,3,4,5,6,7],"operators":["operator","viewer"]}]}' \
  http://localhost:5173/api/duty/groups
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:5173/api/duty/current?area_id=area_gate"
# 二次升级设置：second_after_seconds（0=关闭）
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":0,"severities":["critical"],"auto_dispatch":true,"second_after_seconds":1}' \
  http://localhost:5173/api/settings/escalation
# 注入 critical → 升级后 1s 再升级：GET /api/events 可见 escalated_at 与 re_escalated_at

# P7：用户管理（admin）
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/users
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"username":"operator2","password":"Passw0rd!","display_name":"值班员二","role":"operator","areas":["area_gate"],"active":true}' \
  http://localhost:5173/api/users
# 示例错误：重复用户名 → 409 username_taken；停用/降级/删除自己 → 400 self_protected
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"active":false}' http://localhost:5173/api/users/usr_admin
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/users/usr_admin

# P6：站点配置与 glTF 资源
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/site
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5173/models/site.gltf
# 浏览器打开后顶栏可切换 轨迹 / 视频融合 / 模型细节 / 2.5D底图；
# 场景左下角显示「模型：已加载」或「模型：回退·程序化建筑」。

# P5：总线状态与回放（admin）
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:5173/api/bus/status
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"from_offset":0,"limit":20,"notify":false}' http://localhost:5173/api/bus/replay
# 非法区间 → 400 bad_request；非 admin → 403 forbidden（总线 tab 隐藏）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"from_offset":5,"to_offset":2,"limit":10}' http://localhost:5173/api/bus/replay

# 升级链路手工验证：把 after_seconds 设为 0 并注入一条 critical 检测，等待 ≤15s
# curl -X PUT .../api/settings/escalation -d '{"enabled":true,"after_seconds":0,"severities":["critical"]}'
# curl -X POST .../api/ingest/detections -d '{"camera_code":"CAM-001","detections":[{"class":"smoke","confidence":0.9}]}'
# 随后 GET /api/events 可见 escalated_at，WS 收到 event_updated，/api/notifications 出现 escalation 投递记录。
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
- P3 通知投递依赖可达的 Webhook 接收端（默认示例 `http://127.0.0.1:8099/hook`），失败会记入通知投递记录；升级 worker 扫描周期 15 s，前端不提供手动升级入口。
- 已升级事件的 `escalated_at` 由服务端写入后不可撤销（P3 契约），仅用于展示与通知。
- 值班人员勾选目前使用 P4 种子用户（`admin/operator/viewer`）；后端校验必须是已知用户名，新增用户后需同步前端列表。
- 自动派单依赖升级设置 `enabled + auto_dispatch` 与区域/全局值班组；无匹配值班组时后端审计 `order.auto_dispatch_skipped`，前端不显示工单。
- P5 当前后端驱动为 `jsonl`（可跨重启回放）；`409 bus_driver_unsupported` 仅在不支持回放的驱动或 memory 缓冲区间滚出时出现，界面已映射为 Toast，但当前驱动下无法主动触发。
- 回放条目只进入事件流回放区，不进入三维场景与实时告警统计；回放 feed 上限 100 条。
- P6 底图为前端程序化矢量风格；`map.image_url`（正射影像）当前未渲染（为 null），接入真实影像属后续阶段。
- glTF transform 的 `rotation_y` 按项目约定视为角度（度）；多模型/超大模型未做 Draco/KTX2 压缩解码。
- 视频融合仅对选中相机生效，使用独立隐藏 hls.js 实例（与右侧 CameraVideo 面板互不影响）；关闭/切换相机时释放资源。
- 四个三维开关为会话内存状态，刷新页面恢复默认（轨迹开/视频融合关/模型细节开/2.5D底图开）。
- P7 用户管理为**单租户**；角色/区域/启用状态变更在下次登录生效（JWT 内容不变），停用账号的现有会话在下一次 API 401 时自动登出。
- 用户名创建后不可修改（PATCH 不开放 username）；密码永不返回，编辑时留空表示不修改。
- 用户管理仅 admin 可见/可调用；operator/viewer 的 `GET /api/users` 等均返回 403 forbidden（tab 隐藏）。
- P8 班次时间按服务端 Asia/Shanghai 解释（`at` 可选）；跨夜班次以 `end <= start` 表示；无人班次不派单。
- `rotation=operator` 的轮转为后端进程内轮询（按班组+班次），重启后轮转位置重置；工单去重仅针对同事件未关闭工单。
- 二次升级需 `second_after_seconds > 0`，由后端升级 worker 周期扫描（约 15s）触发；`re_escalated_at` 写入后不可撤销。
- P9 连接质量速率为前端滚动 5s 窗口统计（非后端指标）；空闲超过 5s 会显示琥珀色（属正常，不代表断线）。
- 重连次数自页面加载/上次登录起算；`INGEST_RATE_LIMIT` 默认 0（关闭），未开启时不会出现 429。
