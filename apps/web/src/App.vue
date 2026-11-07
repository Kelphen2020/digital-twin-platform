<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import CameraDetail from './components/CameraDetail.vue'
import EventDetail from './components/EventDetail.vue'
import EventFeed from './components/EventFeed.vue'
import LoginView from './components/LoginView.vue'
import OfflineBanner from './components/OfflineBanner.vue'
import OrdersPanel from './components/OrdersPanel.vue'
import StatsPanel from './components/StatsPanel.vue'
import ToastHost from './components/ToastHost.vue'
import TopBar from './components/TopBar.vue'
import TwinScene from './scene/TwinScene.vue'
import { useAuthStore } from './store/auth'
import { useTwinStore } from './store/twin'

const { state: authState, isAuthenticated, restoreSession } = useAuthStore()
const { state, start, stop, reset, selectCamera, selectEvent, setFps, setSidebarTab } = useTwinStore()

const openOrders = computed(
  () => state.orders.filter((order) => order.status !== 'done' && order.status !== 'cancelled').length,
)

onMounted(() => {
  void restoreSession()
})
onBeforeUnmount(() => {
  stop()
})

// 登录成功 → 启动数据/WS；登出/过期 → 停止并清空历史数据
watch(isAuthenticated, (authenticated) => {
  if (authenticated) start()
  else reset()
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
          @select-camera="selectCamera"
          @open-event="selectEvent"
          @fps="setFps"
        />
        <OfflineBanner />
        <EventDetail />
      </section>

      <aside class="sidebar">
        <CameraDetail />
        <div class="sidebar-tabs" role="tablist">
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
        </div>
        <EventFeed v-if="state.sidebarTab === 'events'" />
        <StatsPanel v-else-if="state.sidebarTab === 'stats'" />
        <OrdersPanel v-else />
      </aside>
    </div>
  </div>

  <ToastHost />
</template>
