<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type Hls from 'hls.js'
import type { Camera } from '../types'

const props = defineProps<{ camera: Camera | null }>()

type PlayerStatus = 'idle' | 'loading' | 'ready' | 'error'

const videoRef = ref<HTMLVideoElement | null>(null)
const status = ref<PlayerStatus>('idle')
const errorMessage = ref('')
const playing = ref(false)
const muted = ref(true)
const quality = ref('')

let hls: Hls | null = null
let videoCleanup: (() => void) | null = null
let loadSeq = 0

const streamUrl = computed(() => props.camera?.stream_url?.trim() ?? '')
const hasStream = computed(() => streamUrl.value.length > 0)
const resolvedUrl = computed(() => {
  if (!streamUrl.value) return ''
  try {
    // 允许相对路径（Vite public 下的 /hls/test.m3u8）与绝对 URL（P1 真实媒体服务）
    return new URL(streamUrl.value, window.location.href).toString()
  } catch {
    return streamUrl.value
  }
})

function setStatus(next: PlayerStatus, message = ''): void {
  status.value = next
  errorMessage.value = message
}

function attachVideoListeners(video: HTMLVideoElement): void {
  const onPlaying = () => {
    playing.value = true
    status.value = 'ready'
  }
  const onPause = () => {
    playing.value = false
  }
  const onError = () => {
    if (status.value !== 'error') {
      setStatus('error', '视频元素报错，可能是编码、网络或 HLS 源不可用')
    }
  }
  video.addEventListener('playing', onPlaying)
  video.addEventListener('pause', onPause)
  video.addEventListener('error', onError)
  videoCleanup = () => {
    video.removeEventListener('playing', onPlaying)
    video.removeEventListener('pause', onPause)
    video.removeEventListener('error', onError)
  }
}

/** 销毁 hls 实例、清空视频源并移除监听（相机切换/卸载时调用）。 */
function teardown(): void {
  loadSeq += 1
  if (videoCleanup) {
    videoCleanup()
    videoCleanup = null
  }
  if (hls) {
    hls.destroy()
    hls = null
  }
  const video = videoRef.value
  if (video) {
    video.pause()
    video.removeAttribute('src')
    video.load()
  }
  playing.value = false
  quality.value = ''
  setStatus('idle')
}

/** 按当前相机 stream_url 重新挂载播放器。 */
async function attach(): Promise<void> {
  teardown()
  const video = videoRef.value
  if (!video || !hasStream.value) return

  const seq = loadSeq
  const url = resolvedUrl.value
  video.muted = muted.value
  attachVideoListeners(video)
  setStatus('loading')

  // Safari/iOS 原生支持 HLS，优先使用原生播放
  if (video.canPlayType('application/vnd.apple.mpegurl') !== '') {
    video.src = url
    const onReady = () => {
      if (seq !== loadSeq) return
      setStatus('ready')
    }
    video.addEventListener('loadedmetadata', onReady, { once: true })
    video.addEventListener('canplay', onReady, { once: true })
    try {
      await video.play()
    } catch {
      // 自动播放被浏览器拦截时保留播放按钮，等待用户手势
    }
    return
  }

  // Chrome/Firefox 等使用 hls.js（按需动态加载，避免增大首屏包体）
  try {
    const module = await import('hls.js')
    if (seq !== loadSeq) return
    const HlsCtor = module.default
    if (!HlsCtor.isSupported()) {
      setStatus('error', '当前浏览器不支持 MSE/HLS 播放')
      return
    }

    const instance = new HlsCtor({
      enableWorker: true,
      lowLatencyMode: true,
      backBufferLength: 30,
    })
    hls = instance

    instance.on(HlsCtor.Events.MANIFEST_PARSED, () => {
      if (seq !== loadSeq) return
      setStatus('ready')
      void video.play().catch(() => {
        // 需要用户手势时由播放按钮接管
      })
    })
    instance.on(HlsCtor.Events.LEVEL_SWITCHED, (_event, data) => {
      const level = instance.levels[data.level]
      if (level?.height) quality.value = `${level.height}p`
    })
    instance.on(HlsCtor.Events.ERROR, (_event, data) => {
      if (seq !== loadSeq) return
      if (data.fatal) {
        setStatus('error', `HLS 播放失败：${data.details}`)
      }
    })

    instance.loadSource(url)
    instance.attachMedia(video)
  } catch (error) {
    if (seq === loadSeq) {
      setStatus('error', `加载 hls.js 失败：${error instanceof Error ? error.message : String(error)}`)
    }
  }
}

async function togglePlay(): Promise<void> {
  const video = videoRef.value
  if (!video || !hasStream.value) return
  if (video.paused) {
    try {
      await video.play()
    } catch {
      // 忽略自动播放限制；保持 ready 状态，用户可再次点击
    }
  } else {
    video.pause()
  }
}

function toggleMute(): void {
  const video = videoRef.value
  if (!video) return
  muted.value = !muted.value
  video.muted = muted.value
}

function retry(): void {
  void attach()
}

watch(
  () => [props.camera?.id, streamUrl.value] as const,
  () => {
    void attach()
  },
)

onMounted(() => {
  void attach()
})

onBeforeUnmount(teardown)
</script>

<template>
  <div class="camera-video">
    <div class="video-frame">
      <video ref="videoRef" class="video-el" playsinline preload="metadata" />

      <div v-if="!hasStream" class="video-placeholder">
        <strong>未配置视频流</strong>
        <span>该相机 stream_url 为空；P1 演示流通常指向 /hls/test.m3u8。</span>
      </div>

      <template v-else>
        <div v-if="status === 'loading'" class="video-overlay">
          <span class="spinner" />
          <span>正在加载直播流…</span>
        </div>
        <div v-else-if="status === 'error'" class="video-overlay video-error">
          <strong>视频加载失败</strong>
          <span>{{ errorMessage }}</span>
          <button class="tiny-btn" @click="retry">重试</button>
        </div>
        <button v-else-if="!playing" class="video-big-play" aria-label="播放" @click="togglePlay">▶</button>

        <div class="video-top">
          <span class="live-badge"><i /> LIVE</span>
          <span v-if="quality" class="quality-badge">{{ quality }}</span>
        </div>
        <div class="video-controls">
          <button class="tiny-btn" @click="togglePlay">{{ playing ? '暂停' : '播放' }}</button>
          <button class="tiny-btn" @click="toggleMute">{{ muted ? '取消静音' : '静音' }}</button>
          <span class="video-source">{{ camera?.code }} · HLS</span>
        </div>
      </template>
    </div>
  </div>
</template>
