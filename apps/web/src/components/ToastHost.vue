<script setup lang="ts">
import { useUiStore } from '../store/ui'

const { state, dismissToast } = useUiStore()
</script>

<template>
  <div class="toast-host" aria-live="polite">
    <div v-for="toast in state.toasts" :key="toast.id" class="toast" :class="`toast-${toast.kind}`">
      <span class="toast-dot" />
      <span class="toast-message">{{ toast.message }}</span>
      <button class="ghost-btn" aria-label="关闭提示" @click="dismissToast(toast.id)">✕</button>
    </div>
  </div>
</template>
