<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  EVENT_ACTION_LABELS,
  EVENT_STATUS_COLORS,
  EVENT_STATUS_LABELS,
  EVENT_TYPE_LABELS,
  ORDER_PRIORITY_LABELS,
  ORDER_PRIORITY_ORDER,
  SEVERITY_COLORS,
  SEVERITY_LABELS,
  formatDateTime,
} from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import type { OrderPriority } from '../types'

const {
  state,
  selectedEvent,
  acknowledgeEvent,
  closeEvent,
  dispatchEvent,
  createOrderFromEvent,
  clearActionError,
  loadEventDetail,
  selectEvent,
} = useTwinStore()
const { can } = useAuthStore()

type FormMode = 'idle' | 'close' | 'dispatch' | 'order'

const mode = ref<FormMode>('idle')
const closeNote = ref('')
const dispatchTarget = ref('安保A组')
const dispatchNote = ref('')
const orderPriority = ref<OrderPriority>('normal')
const orderAssignee = ref('安保A组')
const orderNote = ref('')
const actionSuccess = ref('')

const canHandle = computed(() => can('event:handle'))
const canCreateOrder = computed(() => can('order:create'))

const event = computed(() => state.eventDetail ?? selectedEvent.value)
const actions = computed(() => state.eventActions)
const isClosed = computed(() => event.value?.status === 'closed')
const canAck = computed(() => canHandle.value && event.value?.status === 'new' && state.actionPending === null)
const canClose = computed(() => canHandle.value && !!event.value && event.value.status !== 'closed' && state.actionPending === null)
const canDispatch = computed(() => canHandle.value && !!event.value && state.actionPending === null)
const ackLabel = computed(() => {
  if (state.actionPending === 'ack') return '确认中…'
  if (event.value?.status === 'acked') return '已确认'
  if (isClosed.value) return '已关闭'
  return '确认'
})
const disabledReason = computed(() => (canHandle.value ? '' : '观察员为只读角色，无处置权限'))

// 切换事件时重置本地表单状态
watch(
  () => state.selectedEventId,
  () => {
    mode.value = 'idle'
    closeNote.value = ''
    dispatchTarget.value = '安保A组'
    dispatchNote.value = ''
    orderPriority.value = 'normal'
    orderAssignee.value = '安保A组'
    orderNote.value = ''
    actionSuccess.value = ''
  },
)

async function submitAck(): Promise<void> {
  if (!event.value) return
  actionSuccess.value = ''
  if (await acknowledgeEvent(event.value.id)) actionSuccess.value = '事件已确认'
}

function startClose(): void {
  mode.value = 'close'
  clearActionError()
}

function startDispatch(): void {
  mode.value = 'dispatch'
  clearActionError()
}

function startOrder(): void {
  mode.value = 'order'
  clearActionError()
}

async function submitClose(): Promise<void> {
  if (!event.value) return
  actionSuccess.value = ''
  if (await closeEvent(event.value.id, closeNote.value)) {
    actionSuccess.value = '事件已关闭'
    mode.value = 'idle'
    closeNote.value = ''
  }
}

async function submitDispatch(): Promise<void> {
  if (!event.value) return
  actionSuccess.value = ''
  if (await dispatchEvent(event.value.id, dispatchTarget.value, dispatchNote.value)) {
    actionSuccess.value = '派单已记录'
    mode.value = 'idle'
    dispatchNote.value = ''
  }
}

async function submitOrder(): Promise<void> {
  if (!event.value) return
  actionSuccess.value = ''
  const order = await createOrderFromEvent(event.value.id, {
    priority: orderPriority.value,
    assignee: orderAssignee.value.trim(),
    note: orderNote.value.trim(),
  })
  if (order) {
    actionSuccess.value = `工单已创建：${order.id}`
    mode.value = 'idle'
    orderNote.value = ''
  }
}

function closePanel(): void {
  selectEvent(null)
}
</script>

<template>
  <aside v-if="event" class="event-detail-panel">
    <header class="event-detail-head">
      <div class="event-detail-title">
        <span class="severity-dot" :style="{ background: SEVERITY_COLORS[event.severity] }" />
        <strong>{{ EVENT_TYPE_LABELS[event.type] }}事件</strong>
        <span
          class="status-chip-sm"
          :style="{ color: EVENT_STATUS_COLORS[event.status], borderColor: EVENT_STATUS_COLORS[event.status] }"
        >
          {{ EVENT_STATUS_LABELS[event.status] }}
        </span>
        <span class="event-severity" :style="{ color: SEVERITY_COLORS[event.severity] }">
          {{ SEVERITY_LABELS[event.severity] }}
        </span>
      </div>
      <button class="ghost-btn" aria-label="关闭事件详情" @click="closePanel">✕</button>
    </header>

    <div class="event-detail-body">
      <dl class="detail-grid">
        <div class="wide">
          <dt>事件编号</dt>
          <dd class="mono">{{ event.id }}</dd>
        </div>
        <div>
          <dt>相机</dt>
          <dd>{{ event.camera_code }} · {{ event.camera_name }}</dd>
        </div>
        <div>
          <dt>置信度</dt>
          <dd class="mono">{{ (event.confidence * 100).toFixed(0) }}%</dd>
        </div>
        <div>
          <dt>世界坐标</dt>
          <dd class="mono">E {{ event.x.toFixed(1) }} · N {{ event.y.toFixed(1) }} · U {{ event.z.toFixed(1) }}</dd>
        </div>
        <div>
          <dt>发生时间</dt>
          <dd class="mono">{{ formatDateTime(event.occurred_at) }}</dd>
        </div>
        <div v-if="event.closed_at" class="wide">
          <dt>关闭时间</dt>
          <dd class="mono">{{ formatDateTime(event.closed_at) }}</dd>
        </div>
      </dl>

      <div v-if="state.eventDetailLoading" class="detail-loading">正在加载处置详情…</div>
      <p v-else-if="state.eventDetailError" class="form-error">
        处置详情加载失败：{{ state.eventDetailError }}
        <button class="link-btn" @click="loadEventDetail(event.id)">重试</button>
      </p>

      <p v-if="!canHandle" class="role-notice">观察员为只读角色，无法确认/关闭/派单事件。</p>

      <div class="event-actions">
        <button class="primary-btn" :disabled="!canAck" :title="disabledReason" @click="submitAck">{{ ackLabel }}</button>
        <button class="primary-btn" :disabled="!canClose" :title="disabledReason" @click="startClose">
          {{ isClosed ? '已关闭' : '关闭事件' }}
        </button>
        <button class="primary-btn" :disabled="!canDispatch" :title="disabledReason" @click="startDispatch">派单</button>
      </div>

      <button
        v-if="canCreateOrder"
        class="secondary-btn order-create-btn"
        :disabled="state.orderCreatePending"
        @click="startOrder"
      >
        {{ state.orderCreatePending ? '创建工单中…' : '创建工单' }}
      </button>

      <div v-if="mode === 'close'" class="action-form">
        <label>
          <span>关闭备注（可选）</span>
          <textarea v-model="closeNote" rows="2" placeholder="现场已处置" />
        </label>
        <div class="action-form-buttons">
          <button class="primary-btn" :disabled="state.actionPending !== null" @click="submitClose">提交关闭</button>
          <button class="ghost-btn" @click="mode = 'idle'">取消</button>
        </div>
      </div>

      <div v-if="mode === 'dispatch'" class="action-form">
        <label>
          <span>派单目标（必填）</span>
          <input v-model="dispatchTarget" type="text" placeholder="安保A组" />
        </label>
        <label>
          <span>派单备注（可选）</span>
          <textarea v-model="dispatchNote" rows="2" placeholder="请5分钟内到场" />
        </label>
        <div class="action-form-buttons">
          <button class="primary-btn" :disabled="state.actionPending !== null" @click="submitDispatch">提交派单</button>
          <button class="ghost-btn" @click="mode = 'idle'">取消</button>
        </div>
      </div>

      <div v-if="mode === 'order'" class="action-form">
        <label>
          <span>优先级</span>
          <select v-model="orderPriority">
            <option v-for="priority in ORDER_PRIORITY_ORDER" :key="priority" :value="priority">
              {{ ORDER_PRIORITY_LABELS[priority] }}
            </option>
          </select>
        </label>
        <label>
          <span>负责人 / 处置组（可选）</span>
          <input v-model="orderAssignee" type="text" placeholder="安保A组" />
        </label>
        <label>
          <span>工单备注（可选）</span>
          <textarea v-model="orderNote" rows="2" placeholder="工单处理要求" />
        </label>
        <div class="action-form-buttons">
          <button class="primary-btn" :disabled="state.orderCreatePending" @click="submitOrder">创建工单</button>
          <button class="ghost-btn" @click="mode = 'idle'">取消</button>
        </div>
      </div>

      <p v-if="state.actionError" class="form-error">
        {{ state.actionError }}
        <button class="link-btn" @click="clearActionError">关闭</button>
      </p>
      <p v-if="actionSuccess" class="form-ok">{{ actionSuccess }}</p>

      <div class="action-timeline">
        <h3>处置记录（{{ actions.length }}）</h3>
        <p v-if="actions.length === 0" class="timeline-empty">暂无处置记录</p>
        <ol v-else>
          <li v-for="action in actions" :key="action.id">
            <span class="timeline-dot" />
            <div>
              <div class="timeline-head">
                <strong>{{ EVENT_ACTION_LABELS[action.action] }}</strong>
                <span v-if="action.target" class="timeline-target">→ {{ action.target }}</span>
                <span class="timeline-actor">{{ action.actor }}</span>
                <time>{{ formatDateTime(action.at) }}</time>
              </div>
              <p v-if="action.note" class="timeline-note">{{ action.note }}</p>
            </div>
          </li>
        </ol>
      </div>
    </div>
  </aside>
</template>
