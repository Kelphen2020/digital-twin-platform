<script setup lang="ts">
import { describeApiBase } from '../api'
import { useTwinStore } from '../store/twin'

const { state, loadInitial } = useTwinStore()
</script>

<template>
  <div v-if="!state.backendOnline" class="offline-banner">
    <span class="offline-mark">!</span>
    <div class="offline-copy">
      <strong>{{ state.loading ? '正在连接后端 API…' : '后端 API 不可用' }}</strong>
      <p>
        目标：<span class="mono">{{ describeApiBase() }}</span>
        <template v-if="state.lastError"> · {{ state.lastError }}</template>
        。三维场景仍可浏览，连接恢复后数据会自动加载。
      </p>
    </div>
    <button class="retry-btn" :disabled="state.loading" @click="loadInitial">
      {{ state.loading ? '重试中…' : '立即重试' }}
    </button>
  </div>
</template>
