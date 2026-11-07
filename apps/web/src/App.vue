<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import CameraDetail from './components/CameraDetail.vue'
import EventDetail from './components/EventDetail.vue'
import EventFeed from './components/EventFeed.vue'
import OfflineBanner from './components/OfflineBanner.vue'
import StatsPanel from './components/StatsPanel.vue'
import TopBar from './components/TopBar.vue'
import TwinScene from './scene/TwinScene.vue'
import { useTwinStore } from './store/twin'

const { state, start, stop, selectCamera, selectEvent, setFps, setSidebarTab } = useTwinStore()

onMounted(start)
onBeforeUnmount(stop)
</script>

<template>
  <div class="app-shell">
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
        </div>
        <EventFeed v-if="state.sidebarTab === 'events'" />
        <StatsPanel v-else />
      </aside>
    </div>
  </div>
</template>
