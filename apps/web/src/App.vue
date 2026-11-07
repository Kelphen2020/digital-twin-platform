<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import CameraDetail from './components/CameraDetail.vue'
import EventFeed from './components/EventFeed.vue'
import OfflineBanner from './components/OfflineBanner.vue'
import TopBar from './components/TopBar.vue'
import TwinScene from './scene/TwinScene.vue'
import { useTwinStore } from './store/twin'

const { state, start, stop, selectCamera, selectEvent, setFps } = useTwinStore()

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
          @select-camera="selectCamera"
          @open-event="selectEvent"
          @fps="setFps"
        />
        <OfflineBanner />
      </section>

      <aside class="sidebar">
        <CameraDetail />
        <EventFeed />
      </aside>
    </div>
  </div>
</template>
