<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { formatDateTime } from '../labels'
import { useAdminStore } from '../store/admin'

const { state, loadBusStatus, replayBus } = useAdminStore()

const form = reactive({
  from_offset: 0,
  to_offset: '' as string,
  notify: false,
  limit: 500,
})

const formError = ref('')
const lastResult = ref<{ replayed: number; from: number; to: number } | null>(null)

const status = computed(() => state.busStatus)
const isMemoryDriver = computed(() => (status.value?.driver ?? '').toLowerCase() === 'memory')

function validate(): string | null {
  if (!Number.isInteger(form.from_offset) || form.from_offset < 0) {
    return 'from_offset 需为 ≥ 0 的整数'
  }
  if (form.to_offset !== '') {
    const to = Number(form.to_offset)
    if (!Number.isInteger(to) || to < form.from_offset) {
      return 'to_offset 需为 ≥ from_offset 的整数（留空表示到当前尾部）'
    }
  }
  if (!Number.isInteger(form.limit) || form.limit < 1 || form.limit > 5000) {
    return 'limit 需为 1–5000 的整数（默认 500）'
  }
  return null
}

async function submit(): Promise<void> {
  formError.value = ''
  lastResult.value = null
  const validationError = validate()
  if (validationError) {
    formError.value = validationError
    return
  }
  const result = await replayBus({
    from_offset: form.from_offset,
    to_offset: form.to_offset === '' ? undefined : Number(form.to_offset),
    notify: form.notify,
    limit: form.limit,
  })
  if (result) {
    lastResult.value = { replayed: result.replayed, from: result.from_offset, to: result.to_offset }
  }
}

onMounted(() => {
  void loadBusStatus()
})
</script>

<template>
  <section class="admin-panel">
    <header class="panel-head">
      <h2>事件总线</h2>
      <div class="panel-head-actions">
        <button class="ghost-btn" :disabled="state.busStatusLoading" @click="loadBusStatus">刷新</button>
      </div>
    </header>

    <div class="admin-scroll">
      <p v-if="state.busStatusLoading && !status" class="detail-loading">正在读取总线状态…</p>
      <p v-else-if="state.busStatusError" class="form-error">
        总线状态读取失败：{{ state.busStatusError }}
        <button class="link-btn" @click="loadBusStatus">重试</button>
      </p>

      <template v-else-if="status">
        <div class="bus-grid">
          <div class="bus-card">
            <span>驱动（BUS_DRIVER）</span>
            <strong>{{ status.driver }}</strong>
          </div>
          <div class="bus-card">
            <span>已发布</span>
            <strong>{{ status.published }}</strong>
          </div>
          <div class="bus-card">
            <span>已回放</span>
            <strong>{{ status.replayed }}</strong>
          </div>
          <div class="bus-card">
            <span>订阅者</span>
            <strong>{{ status.subscribers }}</strong>
          </div>
          <div class="bus-card">
            <span>缓冲起点 offset</span>
            <strong>{{ status.oldest_offset }}</strong>
          </div>
          <div class="bus-card">
            <span>下一条 offset</span>
            <strong>{{ status.next_offset }}</strong>
          </div>
          <div class="bus-card wide">
            <span>最近发布</span>
            <strong class="bus-time">{{ status.last_publish_at ? formatDateTime(status.last_publish_at) : '—' }}</strong>
          </div>
          <div class="bus-card wide">
            <span>存储路径</span>
            <strong class="bus-path">{{ status.path || '—（memory 驱动为进程内环形缓冲）' }}</strong>
          </div>
        </div>

        <section class="settings-section">
          <header class="settings-head">
            <h3>回放事件</h3>
            <span class="text-faint">{{ isMemoryDriver ? 'memory 驱动' : 'jsonl 驱动' }}</span>
          </header>

          <div class="settings-grid">
            <label class="settings-field">
              <span>from_offset（≥ 0）</span>
              <input v-model.number="form.from_offset" type="number" min="0" step="1" />
            </label>
            <label class="settings-field">
              <span>to_offset（可选，留空=到当前尾部）</span>
              <input v-model.number="form.to_offset" type="number" min="0" step="1" placeholder="留空" />
            </label>
            <label class="settings-field">
              <span>单次条数上限 limit（1–5000）</span>
              <input v-model.number="form.limit" type="number" min="1" max="5000" step="1" />
            </label>
          </div>

          <label class="switch settings-switch">
            <input v-model="form.notify" type="checkbox" />
            <span>同时触发 Webhook 通知（默认关闭，避免回放刷屏）</span>
          </label>

          <p class="form-hint">
            回放会通过 WS 重新推送：`event_created` → `event_replay`（事件流显示「回放」徽标与 offset），
            `event_escalated` → `event_updated`。回放条目不计入实时告警与事件总数。
          </p>
          <p v-if="formError" class="form-error">{{ formError }}</p>
          <p v-if="state.busReplayError" class="form-error">{{ state.busReplayError }}</p>
          <p v-if="lastResult" class="form-ok">
            已回放 {{ lastResult.replayed }} 条（offset {{ lastResult.from }}–{{ lastResult.to }}）
          </p>

          <div class="settings-actions">
            <button class="primary-btn" :disabled="state.busReplayPending" @click="submit">
              {{ state.busReplayPending ? '回放中…' : '开始回放' }}
            </button>
            <button class="ghost-btn" :disabled="state.busStatusLoading" @click="loadBusStatus">刷新状态</button>
          </div>
        </section>
      </template>
    </div>
  </section>
</template>
