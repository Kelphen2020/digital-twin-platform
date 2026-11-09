<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AuditPanel from './components/AuditPanel.vue'
import BusPanel from './components/BusPanel.vue'
import UserPanel from './components/UserPanel.vue'
import CameraDetail from './components/CameraDetail.vue'
import DutyPanel from './components/DutyPanel.vue'
import EventDetail from './components/EventDetail.vue'
import EventFeed from './components/EventFeed.vue'
import LoginView from './components/LoginView.vue'
import OfflineBanner from './components/OfflineBanner.vue'
import OrdersPanel from './components/OrdersPanel.vue'
import PatrolPanel from './components/PatrolPanel.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import StatsPanel from './components/StatsPanel.vue'
import ToastHost from './components/ToastHost.vue'
import TopBar from './components/TopBar.vue'
import { formatArea } from './labels'
import TwinScene from './scene/TwinScene.vue'
import { useAdminStore } from './store/admin'
import { useAuthStore } from './store/auth'
import { useDutyStore } from './store/duty'
import { usePatrolStore } from './store/patrol'
import { useTwinStore } from './store/twin'

const { state: authState, isAuthenticated, restoreSession } = useAuthStore()
const { state, start, stop, reset, selectCamera, selectEvent, setFps, setSidebarTab } = useTwinStore()
const admin = useAdminStore()
const duty = useDutyStore()
const patrol = usePatrolStore()

// ---- P12：测距 / 框选 / 巡检面板 ----
const measureMode = ref(false)
const patrolOpen = ref(false)
const boxSelectedCameraIds = ref<string[]>([])
const patrolRoute = computed(() => patrol.selectedRoute.value)
const patrolPendingCount = computed(() => patrol.pendingCount.value)

function toggleMeasureMode(): void {
  measureMode.value = !measureMode.value
}

function handleBoxSelect(cameraIds: string[]): void {
  boxSelectedCameraIds.value = cameraIds
}

const isAdmin = computed(() => authState.user?.role === 'admin')

const openOrders = computed(
  () => state.orders.filter((order) => order.status !== 'done' && order.status !== 'cancelled').length,
)

/** 非 admin 且负责区域非空时，后端只返回区域内数据 —— 侧栏显示提示 */
const areaHint = computed(() => {
  const user = authState.user
  if (!user || user.role === 'admin') return ''
  const areas = user.areas ?? []
  return areas.length > 0 ? areas.map(formatArea).join('、') : ''
})

onMounted(() => {
  void restoreSession()
})
onBeforeUnmount(() => {
  stop()
})

// 登录成功 → 启动数据/WS；登出/过期 → 停止并清空历史数据
watch(isAuthenticated, (authenticated) => {
  if (authenticated) {
    start()
    void patrol.loadExecutions()
  }
  else {
    reset()
    admin.reset()
    duty.reset()
    patrol.reset()
    measureMode.value = false
    patrolOpen.value = false
    boxSelectedCameraIds.value = []
  }
})
</script>

<template>
  <div v-if="authState.booting" class="boot-screen">
    <span class="spinner" />
    <span>正在验证登录状态…</span>
  </div>

  <LoginView v-else-if="!isAuthenticated" />

  <div v-else class="app-shell">
    <TopBar />

    <div class="app-body">
      <section class="viewport">
        <TwinScene
          :cameras="state.cameras"
          :events="state.events"
          :selected-camera-id="state.selectedCameraId"
          :selected-event-id="state.selectedEventId"
          :focus-request="state.focusRequest"
          :offline="!state.backendOnline"
          :tracks="state.tracks"
          :show-tracks="state.showTracks"
          :site="state.site"
          :areas="state.areas"
          :show-base-map="state.showBaseMap"
          :show-model-details="state.showModelDetails"
          :show-video-fusion="state.showVideoFusion"
          :measure-mode="measureMode"
          :patrol-route="patrolRoute"
          :patrol-playing="patrol.state.playing"
          :patrol-index="patrol.state.currentIndex"
          :patrol-pending-count="patrolPendingCount"
          @select-camera="selectCamera"
          @open-event="selectEvent"
          @fps="setFps"
          @toggle-measure="toggleMeasureMode"
          @open-patrol="patrolOpen = true"
          @box-select="handleBoxSelect"
        />
        <PatrolPanel
          v-if="patrolOpen"
          :box-selected-camera-ids="boxSelectedCameraIds"
          @close="patrolOpen = false"
        />
        <OfflineBanner />
        <EventDetail />
      </section>

      <aside class="sidebar">
        <p v-if="areaHint" class="area-scope-hint">
          <span class="area-scope-dot" />
          仅显示负责区域：{{ areaHint }}
        </p>
        <CameraDetail />
        <div class="sidebar-tabs" :class="{ 'admin-tabs': isAdmin }" role="tablist">
          <button
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'events' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'events'"
            @click="setSidebarTab('events')"
          >
            事件流
          </button>
          <button
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'stats' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'stats'"
            @click="setSidebarTab('stats')"
          >
            统计
          </button>
          <button
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'orders' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'orders'"
            @click="setSidebarTab('orders')"
          >
            工单
            <span v-if="openOrders > 0" class="tab-badge">{{ openOrders }}</span>
          </button>
          <button
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'duty' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'duty'"
            @click="setSidebarTab('duty')"
          >
            值班
          </button>
          <button
            v-if="isAdmin"
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'settings' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'settings'"
            @click="setSidebarTab('settings')"
          >
            设置
          </button>
          <button
            v-if="isAdmin"
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'audit' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'audit'"
            @click="setSidebarTab('audit')"
          >
            审计
          </button>
          <button
            v-if="isAdmin"
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'bus' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'bus'"
            @click="setSidebarTab('bus')"
          >
            总线
          </button>
          <button
            v-if="isAdmin"
            class="sidebar-tab"
            :class="{ active: state.sidebarTab === 'users' }"
            role="tab"
            :aria-selected="state.sidebarTab === 'users'"
            @click="setSidebarTab('users')"
          >
            用户
          </button>
        </div>
        <EventFeed v-if="state.sidebarTab === 'events'" />
        <StatsPanel v-else-if="state.sidebarTab === 'stats'" />
        <OrdersPanel v-else-if="state.sidebarTab === 'orders'" />
        <DutyPanel v-else-if="state.sidebarTab === 'duty'" />
        <SettingsPanel v-else-if="state.sidebarTab === 'settings' && isAdmin" />
        <AuditPanel v-else-if="state.sidebarTab === 'audit' && isAdmin" />
        <BusPanel v-else-if="state.sidebarTab === 'bus' && isAdmin" />
        <UserPanel v-else-if="state.sidebarTab === 'users' && isAdmin" />
        <EventFeed v-else />
      </aside>
    </div>
  </div>

  <ToastHost />
</template>
