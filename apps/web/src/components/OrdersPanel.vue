<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ORDER_PRIORITY_COLORS,
  ORDER_PRIORITY_LABELS,
  ORDER_STATUS_COLORS,
  ORDER_STATUS_LABELS,
  ORDER_STATUS_ORDER,
  formatDateTime,
  formatRelativeTime,
} from '../labels'
import { useAuthStore } from '../store/auth'
import { useTwinStore } from '../store/twin'
import type { Order, OrderStatus } from '../types'

const { state, filteredOrders, loadOrders, updateOrderFields, setOrderStatusFilter } = useTwinStore()
const { can } = useAuthStore()

interface OrderEditState {
  status: OrderStatus
  assignee: string
  note: string
}

const expandedId = ref<string | null>(null)
const editStates = reactive<Record<string, OrderEditState>>({})
const nowTick = ref(Date.now())

const canUpdate = computed(() => can('order:update'))

const statusOptions: { value: OrderStatus | 'all'; label: string }[] = [
  { value: 'all', label: '全部状态' },
  ...ORDER_STATUS_ORDER.map((status) => ({ value: status, label: ORDER_STATUS_LABELS[status] })),
]

function onFilterChange(event: Event): void {
  setOrderStatusFilter((event.target as HTMLSelectElement).value as OrderStatus | 'all')
}

function editState(order: Order): OrderEditState {
  const existing = editStates[order.id]
  if (existing) return existing
  const created: OrderEditState = { status: order.status, assignee: order.assignee, note: order.note }
  editStates[order.id] = created
  return created
}

function toggle(order: Order): void {
  if (expandedId.value === order.id) {
    expandedId.value = null
    return
  }
  expandedId.value = order.id
  editStates[order.id] = { status: order.status, assignee: order.assignee, note: order.note }
}

async function save(order: Order): Promise<void> {
  const edit = editState(order)
  const ok = await updateOrderFields(order.id, {
    status: edit.status,
    assignee: edit.assignee.trim(),
    note: edit.note.trim(),
  })
  if (ok) expandedId.value = null
}

onMounted(() => {
  void loadOrders()
  window.setInterval(() => {
    nowTick.value = Date.now()
  }, 10_000)
})
</script>

<template>
  <section class="orders-panel">
    <header class="panel-head">
      <h2>工单</h2>
      <div class="panel-head-actions">
        <span class="feed-count">{{ filteredOrders.length }}</span>
        <button class="ghost-btn" :disabled="state.ordersLoading" @click="loadOrders">刷新</button>
      </div>
    </header>

    <div class="orders-filter">
      <label>
        <span>状态</span>
        <select :value="state.orderStatusFilter" @change="onFilterChange">
          <option v-for="option in statusOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
    </div>

    <div class="orders-list">
      <div v-if="state.ordersLoading && state.orders.length === 0" class="detail-loading">正在加载工单…</div>
      <p v-else-if="state.ordersError" class="form-error">
        工单加载失败：{{ state.ordersError }}
        <button class="link-btn" @click="loadOrders">重试</button>
      </p>
      <div v-else-if="filteredOrders.length === 0" class="feed-empty">
        <p>暂无工单</p>
        <span>{{ state.orderStatusFilter === 'all' ? '在事件处置面板中点击「创建工单」即可从告警生成工单。' : '当前状态下没有工单，调整状态筛选查看其他工单。' }}</span>
      </div>

      <article
        v-for="order in filteredOrders"
        :key="order.id"
        class="order-item"
        :class="{ expanded: expandedId === order.id }"
        @click="toggle(order)"
      >
        <div class="order-main">
          <div class="order-head">
            <span class="order-title">{{ order.title || order.id }}</span>
            <span
              class="status-chip-sm"
              :style="{ color: ORDER_STATUS_COLORS[order.status], borderColor: ORDER_STATUS_COLORS[order.status] }"
            >
              {{ ORDER_STATUS_LABELS[order.status] }}
            </span>
            <span class="order-priority" :style="{ color: ORDER_PRIORITY_COLORS[order.priority] }">
              {{ ORDER_PRIORITY_LABELS[order.priority] }}优先
            </span>
          </div>
          <p class="order-meta mono">
            {{ order.camera_id }} · 事件 {{ order.event_id }} · {{ formatRelativeTime(order.created_at, nowTick) }}
          </p>
          <p class="order-meta">
            负责人：<span :class="{ 'text-faint': !order.assignee }">{{ order.assignee || '未分配' }}</span>
          </p>
          <p v-if="order.note" class="order-meta order-note" :title="order.note">{{ order.note }}</p>
          <p v-if="order.closed_at" class="order-meta closed-line">关闭时间：{{ formatDateTime(order.closed_at) }}</p>
        </div>

        <div v-if="expandedId === order.id" class="order-edit" @click.stop>
          <template v-if="canUpdate">
            <div class="order-edit-row">
              <label>
                <span>状态</span>
                <select v-model="editState(order).status">
                  <option v-for="status in ORDER_STATUS_ORDER" :key="status" :value="status">
                    {{ ORDER_STATUS_LABELS[status] }}
                  </option>
                </select>
              </label>
              <label>
                <span>负责人</span>
                <input v-model="editState(order).assignee" type="text" placeholder="安保A组" />
              </label>
            </div>
            <label class="order-edit-note">
              <span>备注</span>
              <textarea v-model="editState(order).note" rows="2" placeholder="处理记录" />
            </label>
            <div class="order-edit-actions">
              <button class="primary-btn" :disabled="state.orderActionPendingId !== null" @click="save(order)">
                {{ state.orderActionPendingId === order.id ? '保存中…' : '保存工单' }}
              </button>
              <button class="ghost-btn" @click="expandedId = null">收起</button>
            </div>
          </template>
          <template v-else>
            <dl class="detail-grid order-readonly">
              <div>
                <dt>状态</dt>
                <dd>{{ ORDER_STATUS_LABELS[order.status] }}</dd>
              </div>
              <div>
                <dt>负责人</dt>
                <dd>{{ order.assignee || '未分配' }}</dd>
              </div>
              <div class="wide">
                <dt>备注</dt>
                <dd>{{ order.note || '—' }}</dd>
              </div>
            </dl>
            <p class="role-notice">观察员为只读角色，无法修改工单。</p>
          </template>
        </div>
      </article>
    </div>

    <footer class="feed-foot">工单状态：待处理 → 已派发 → 处理中 → 已完成 / 已取消 · 实时接收 WS order_updated</footer>
  </section>
</template>
