/**
 * 与 docs/API.md + docs/API_P1.md + docs/API_P2.md 对齐的数据模型与 WS 消息类型。
 * 坐标统一为局部 ENU 米制：x 东、y 北、z 上；时间统一 RFC3339 UTC 字符串。
 */

export type CameraStatus = 'online' | 'offline' | 'degraded'

export type EventType = 'person' | 'vehicle' | 'intrusion' | 'smoke' | 'no_helmet' | 'other'

export type Severity = 'info' | 'warn' | 'critical'

export type EventStatus = 'new' | 'acked' | 'closed'

/** 相机单应矩阵标定结果（P1，可空）。 */
export interface CameraCalibration {
  /** 3×3 单应矩阵：像素 → ENU。 */
  matrix: number[][]
  rmse: number
  max_residual?: number | null
  points_count: number
  solved_at?: string | null
}

export interface Camera {
  id: string
  code: string
  name: string
  area_id: string
  x: number
  y: number
  z: number
  heading: number
  fov_h: number
  fov_v: number
  range_m: number
  status: CameraStatus
  stream_url: string | null
  calibration?: CameraCalibration | null
  updated_at: string
}

/** PATCH /api/cameras/{id} 允许修改的字段（P0 契约）。 */
export interface CameraUpdatePayload {
  status?: CameraStatus
  x?: number
  y?: number
  z?: number
  heading?: number
}

export interface TwinEvent {
  id: string
  camera_id: string
  camera_code: string
  camera_name: string
  type: EventType
  severity: Severity
  confidence: number
  x: number
  y: number
  z: number
  occurred_at: string
  status: EventStatus
  closed_at?: string | null
  /** 告警升级时间（P3，可空）；非空表示超时未处置已升级。 */
  escalated_at?: string | null
  /** 二次升级时间（P8，可空）；非空表示升级后超时仍未关闭。 */
  re_escalated_at?: string | null
  payload: Record<string, unknown> | null
}

/** 事件处置动作（审计时间线）。 */
export type EventActionType = 'ack' | 'close' | 'dispatch'

export interface EventAction {
  id: string
  event_id: string
  action: EventActionType
  actor: string
  target: string | null
  note: string | null
  at: string
}

/** 轨迹点（P1）。class 与事件类型取值一致。 */
export interface TrackPoint {
  track_id: string
  camera_id: string
  class: EventType
  x: number
  y: number
  z: number
  ts: string
}

/** 相机标定控制点（像素坐标 + 世界 ENU）。 */
export interface ControlPoint {
  px: number
  py: number
  x: number
  y: number
}

export interface CalibrationSolveResponse {
  matrix: number[][]
  rmse: number
  max_residual: number
  points_count: number
}

export interface CalibrationSavePayload {
  matrix: number[][]
  rmse: number
  max_residual?: number | null
  points_count: number
}

export interface CameraListResponse {
  items: Camera[]
}

export interface EventListResponse {
  items: TwinEvent[]
  total: number
}

export interface EventDetailResponse {
  event: TwinEvent
  actions: EventAction[]
}

export interface TrackListResponse {
  items: TrackPoint[]
}

export interface StatsTotals {
  events: number
  by_severity: Partial<Record<Severity, number>>
  by_type: Partial<Record<EventType, number>>
}

export interface StatsCameraRow {
  camera_id: string
  code: string
  name: string
  count: number
}

export interface StatsHourRow {
  hour: string
  count: number
}

/** GET /api/stats/summary 响应（P1）。 */
export interface StatsSummary {
  from: string
  to: string
  totals: StatsTotals
  by_camera: StatsCameraRow[]
  by_hour: StatsHourRow[]
}

export type StatsRange = '1h' | '24h'

// ---------------- P2：身份与 RBAC ----------------

export type Role = 'viewer' | 'operator' | 'admin' | 'tenant_admin'

export interface AuthUser {
  id: string
  username: string
  display_name: string
  role: Role
  /** 负责区域；admin/viewer 可能为 null（表示全部/只读）。 */
  areas: string[] | null
}

export interface LoginResponse {
  token: string
  expires_at: string
  /** P27：登录租户 id（缺省 default）。 */
  tenant_id?: string
  /** P11：refresh token（轮换，服务端只存哈希）。 */
  refresh_token?: string
  /** P11：refresh token 过期时间（RFC3339）。 */
  refresh_expires_at?: string
  user: AuthUser
}

/** POST /api/auth/refresh（不含 user；refresh_token 为轮换后的新值）。 */
export interface RefreshResponse {
  token: string
  expires_at?: string
  refresh_token: string
  refresh_expires_at?: string
}

/** GET /api/users/{id}/sessions（admin）。 */
export interface UserSession {
  id: string
  created_at: string
  expires_at: string
  ip: string
  user_agent: string
}

export interface UserSessionsResponse {
  items: UserSession[]
}

/** 前端可判定的操作权限键（与后端角色矩阵一致）。 */
export type Permission =
  | 'event:handle'
  | 'order:create'
  | 'order:update'
  | 'camera:update'
  | 'camera:calibration'
  | 'camera:rules'

// ---------------- P2：相机规则 ----------------

export interface CameraRules {
  camera_id: string
  confidence_threshold: number
  dedupe_seconds: number
  enabled_classes: EventType[]
  /** 归一化图像坐标多边形；null 表示全画面。 */
  roi: [number, number][] | null
  updated_at: string
}

export type CameraRulesPayload = Pick<
  CameraRules,
  'confidence_threshold' | 'dedupe_seconds' | 'enabled_classes' | 'roi'
>

// ---------------- P2：工单 ----------------

export type OrderStatus = 'open' | 'assigned' | 'processing' | 'done' | 'cancelled'

export type OrderPriority = 'low' | 'normal' | 'high' | 'urgent'

export interface Order {
  id: string
  event_id: string
  camera_id: string
  title: string
  status: OrderStatus
  priority: OrderPriority
  assignee: string
  note: string
  created_at: string
  updated_at: string
  closed_at?: string | null
}

export interface CreateOrderPayload {
  event_id: string
  title?: string
  priority?: OrderPriority
  assignee?: string
  note?: string
}

export interface UpdateOrderPayload {
  status?: OrderStatus
  assignee?: string
  note?: string
}

export interface OrderListResponse {
  items: Order[]
}

// ---------------- WS ----------------

/** WebSocket 推送消息（服务端 → 客户端，P0 + P1 + P2）。 */
export interface WsHelloMessage {
  type: 'hello'
  data: { server_time: string; cameras: number }
}

export interface WsEventMessage {
  type: 'event'
  data: TwinEvent
}

export interface WsEventUpdatedMessage {
  type: 'event_updated'
  data: TwinEvent
}

export interface WsCameraStatusMessage {
  type: 'camera_status'
  data: { id: string; code: string; status: CameraStatus }
}

export interface WsTrackPointMessage {
  type: 'track_point'
  data: TrackPoint
}

export interface WsOrderUpdatedMessage {
  type: 'order_updated'
  data: Order
}

/** P5：总线回放帧（重放的 event_created），data 为 { event, offset, replay }。 */
export interface WsEventReplayMessage {
  type: 'event_replay'
  data: {
    event: TwinEvent
    offset: number
    replay: boolean
  }
}

export interface WsStatsMessage {
  type: 'stats'
  data: { events_total: number; online_cameras: number; ws_clients: number }
}

export type WsMessage =
  | WsHelloMessage
  | WsEventMessage
  | WsEventUpdatedMessage
  | WsCameraStatusMessage
  | WsTrackPointMessage
  | WsOrderUpdatedMessage
  | WsEventReplayMessage
  | WsPatrolExecutionMessage
  | WsStatsMessage

export type WsState = 'connecting' | 'online' | 'offline'

export interface EventFilters {
  type: EventType | 'all'
  severity: Severity | 'all'
}

// ---------------- P3：设置 / 审计 / 通知投递 ----------------

export type NotificationFormat = 'generic' | 'feishu'

/** GET/PUT /api/settings/notifications（admin；P4 增加重试/去抖字段）。 */
export interface NotificationSettings {
  enabled: boolean
  webhook_url: string
  format: NotificationFormat
  min_severity: Severity
  timeout_seconds: number
  /** 最大尝试次数（1–5，含首次）。 */
  max_attempts: number
  /** 指数退避基数秒：第 n 次重试等待 backoff_seconds × 2^(n-1)。 */
  backoff_seconds: number
  /** 同相机+类型+严重级别的通知静默窗口（0–3600s）。 */
  silence_seconds: number
}

/** GET/PUT /api/settings/escalation（admin；P4 自动派单，P8 二次升级）。 */
export interface EscalationSettings {
  enabled: boolean
  after_seconds: number
  severities: Severity[]
  /** 升级时是否按相机区域解析值班组并自动创建工单。 */
  auto_dispatch: boolean
  /** 升级后再超过该时长仍未关闭则触发二次升级；0 表示关闭（0–604800 秒）。 */
  second_after_seconds: number
  /** P15：告警首次升级/再升级时自动生成待执行巡检任务。 */
  auto_patrol: boolean
}

/** GET /api/audit 审计条目。 */
export interface AuditEntry {
  id: string
  at: string
  actor: string
  action: string
  resource: string
  detail: Record<string, unknown> | null
}

export interface AuditListResponse {
  items: AuditEntry[]
}

/** GET /api/notifications 通知投递记录。 */
export interface NotificationLogEntry {
  id: string
  at: string
  kind: string
  event_id: string
  url: string
  format: string
  ok: boolean
  status_code: number
  error: string
  /** 实际尝试次数（P4，成功或最终失败）。 */
  attempts: number
}

export interface NotificationListResponse {
  items: NotificationLogEntry[]
}

// ---------------- P4：值班组 / 区域 ----------------

/** GET /api/areas 区域（P0 契约，P4 值班组区域下拉使用）。 */
export interface Area {
  id: string
  name: string
  type: string
  polygon?: number[][]
}

export interface AreaListResponse {
  items: Area[]
}

/** P8：值班班次（weekdays 为 ISO 周几：1=周一 … 7=周日）。 */
export interface DutyShift {
  /** 服务端生成的班次 id；新建时缺省。 */
  id?: string
  name: string
  /** HH:MM */
  start: string
  /** HH:MM；end <= start 视为跨夜。 */
  end: string
  weekdays: number[]
  operators: string[]
}

export type DutyRotation = 'group' | 'operator'

export interface DutyGroup {
  id: string
  name: string
  /** 负责区域；空数组表示全局兜底值班组。 */
  areas: string[]
  /** 值班人员用户名（须为已知用户）。 */
  operators: string[]
  active: boolean
  /** P8：派单对象——group=班组名，operator=按班次成员轮转。 */
  rotation?: DutyRotation
  /** P8：班次列表；为空时沿用 operators 全天值班。 */
  shifts?: DutyShift[]
  created_at: string
  updated_at: string
}

export interface DutyGroupPayload {
  name: string
  areas: string[]
  operators: string[]
  active: boolean
  rotation: DutyRotation
  shifts: DutyShift[]
}

export interface DutyListResponse {
  items: DutyGroup[]
}

/** GET /api/duty/current?area_id= */
export interface DutyCurrent {
  area_id: string
  group: DutyGroup | null
  /** P8：当前生效班次（无班次/非值班时间为 null）。 */
  shift?: { id?: string; name: string; start: string; end: string } | null
  operators: string[]
}

// ---------------- P13：巡检计划与执行 ----------------

export type PatrolExecutionStatus = 'pending' | 'running' | 'done' | 'issue' | 'cancelled'

export interface PatrolSchedule {
  id: string
  name: string
  route_id: string
  interval_minutes: number
  enabled: boolean
  created_by: string
  created_at: string
  updated_at: string
  next_run_at: string
}

export interface PatrolSchedulePayload {
  name: string
  route_id: string
  interval_minutes: number
  enabled: boolean
}

export interface PatrolExecution {
  id: string
  schedule_id: string
  route_id: string
  route_name: string
  /** P15：告警联动生成的任务对应事件 id；空表示计划/手动任务。 */
  event_id?: string
  status: PatrolExecutionStatus
  assignee: string
  result: string
  note: string
  order_id: string
  scheduled_at: string
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export interface PatrolExecutionListResponse {
  items: PatrolExecution[]
}

export interface PatrolExecutionCompletePayload {
  result: 'done' | 'issue'
  note: string
}

export interface PatrolStatsRoute {
  route_id: string
  route_name: string
  total: number
  done: number
  issue: number
}

export interface PatrolStatsAssignee {
  assignee: string
  total: number
  done: number
  issue: number
}

/** GET /api/patrol/stats?from=&to=（P14）。 */
export interface PatrolStats {
  from?: string | null
  to?: string | null
  executions_total: number
  pending: number
  running: number
  done: number
  issue: number
  /** P16：已取消/超时关闭任务数。 */
  cancelled: number
  completion_rate: number
  issue_rate: number
  avg_duration_seconds: number
  by_route: PatrolStatsRoute[]
  by_assignee: PatrolStatsAssignee[]
}

/** WS：巡检执行状态变化（P13；后端广播则刷新列表）。 */
export interface WsPatrolExecutionMessage {
  type: 'patrol_execution'
  data: PatrolExecution
}

// ---------------- P27：多租户 ----------------

export interface TenantSeeded {
  cameras: number
  areas: number
  rules: number
  settings: number
  /** 初始管理员用户名；未创建为空字符串。 */
  admin: string
}

export interface Tenant {
  id: string
  name: string
  active: boolean
  /** P29：创建响应中的模板复制/初始管理员结果。 */
  seeded?: TenantSeeded
}

export interface TenantListResponse {
  items: Tenant[]
}

export interface TenantCreatePayload {
  name: string
  /** P29：复制模板的租户 id（缺省不复制）。 */
  template?: string
  /** P29：可选初始管理员（密码走 P10 策略）。 */
  admin?: { username: string; password: string }
}

export interface TenantUpdatePayload {
  name?: string
  active?: boolean
}

// ---------------- P12：巡检路线 ----------------

export interface PatrolWaypoint {
  /** ENU 米制点位。 */
  x: number
  y: number
  z: number
  /** 视角方位角（度，默认 0）。 */
  heading?: number
}

export interface PatrolRoute {
  id: string
  name: string
  waypoints: PatrolWaypoint[]
  /** 用于播放时高亮联动的相机 id。 */
  camera_ids: string[]
  /** 航点间飞行/停留秒数（1–60）。 */
  interval_seconds: number
  created_by: string
  created_at: string
  updated_at: string
}

export interface PatrolRoutePayload {
  name: string
  waypoints: PatrolWaypoint[]
  camera_ids: string[]
  interval_seconds: number
}

export interface PatrolRouteListResponse {
  items: PatrolRoute[]
}

// ---------------- P7：用户与账号管理 ----------------

/** GET /api/users 用户对象（密码永不返回）。 */
export interface ManagedUser {
  id: string
  username: string
  display_name: string
  role: Role
  /** 负责区域；空/ null 表示全部区域。 */
  areas: string[] | null
  active?: boolean
  created_at?: string
  updated_at?: string
}

export interface UserListResponse {
  items: ManagedUser[]
}

export interface CreateUserPayload {
  username: string
  password: string
  display_name: string
  role: Role
  areas: string[]
  active: boolean
}

export interface UpdateUserPayload {
  display_name?: string
  password?: string
  role?: Role
  areas?: string[]
  active?: boolean
}

// ---------------- P6：三维站点配置 ----------------

export interface SiteOrigin {
  lat: number
  lng: number
  alt: number
}

/** 站点局部 ENU 米制范围。 */
export interface SiteExtent {
  min_x: number
  max_x: number
  min_y: number
  max_y: number
  min_z: number
  max_z: number
}

export interface SiteModelTransform {
  x: number
  y: number
  z: number
  /** 绕 ENU 上轴（z）旋转角度（度）。 */
  rotation_y: number
  scale: number
}

export interface SiteModel {
  id: string
  name: string
  format: string
  url: string
  transform: SiteModelTransform
}

export interface SiteMapConfig {
  style: string
  image_url: string | null
}

/** GET /api/site（登录用户）。 */
export interface SiteConfig {
  name: string
  origin: SiteOrigin
  extent: SiteExtent
  models: SiteModel[]
  map: SiteMapConfig
}

/** glTF 模型加载状态（用于场景内“模型已加载/回退”指示）。 */
export type SiteModelStatus = 'idle' | 'loading' | 'loaded' | 'partial' | 'fallback'

// ---------------- P5：可回放事件总线 ----------------

/** GET /api/bus/status（admin）。 */
export interface BusStatus {
  driver: string
  /** jsonl 驱动为文件路径；memory 驱动为空字符串。 */
  path: string
  published: number
  replayed: number
  oldest_offset: number
  next_offset: number
  last_publish_at: string
  subscribers: number
}

/** POST /api/bus/replay 请求。 */
export interface BusReplayRequest {
  from_offset: number
  to_offset?: number
  notify: boolean
  limit: number
}

/** POST /api/bus/replay 响应。 */
export interface BusReplayResponse {
  ok: boolean
  replayed: number
  from_offset: number
  to_offset: number
}

/** 前端事件流中的回放条目（不进入实时事件/三维标记）。 */
export interface ReplayFeedItem {
  key: string
  event: TwinEvent
  offset: number
  receivedAt: number
}

export type SidebarTab = 'events' | 'stats' | 'orders' | 'duty' | 'settings' | 'audit' | 'bus' | 'users' | 'tenant'

/** 场景视角聚焦请求（由事件流/详情面板触发，场景消费后播放运镜动画）。 */
export interface FocusRequest {
  cameraId: string
  seq: number
}
