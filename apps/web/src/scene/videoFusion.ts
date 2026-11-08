import type Hls from 'hls.js'
import * as THREE from 'three'
import type { Camera } from '../types'
import { enuToThree, headingToThreeDirection } from './coordinates'
import { disposeObject3D } from './dispose'
import { createTextSprite } from './textSprite'

/** 视频面位于相机朝向上量程的 60% 处。 */
const FUSION_DISTANCE_RATIO = 0.6

export interface VideoFusion {
  group: THREE.Group
  dispose(): void
}

/**
 * 视频纹理融合：为选中相机创建独立隐藏 <video> + hls.js 实例，
 * 把 VideoTexture 贴到视锥内的平面（按 fov_h/fov_v 与 range 计算尺寸），
 * 带边框与暗角。独立于右侧 CameraVideo 播放器。
 */
export async function createVideoFusion(camera: Camera): Promise<VideoFusion | null> {
  const streamUrl = camera.stream_url?.trim()
  if (!streamUrl) return null

  const url = new URL(streamUrl, window.location.href).toString()
  const video = document.createElement('video')
  video.muted = true
  video.playsInline = true
  video.preload = 'auto'
  video.style.position = 'fixed'
  video.style.left = '-10000px'
  video.style.top = '0'
  video.style.width = '2px'
  video.style.height = '2px'
  video.style.opacity = '0'
  video.style.pointerEvents = 'none'
  document.body.appendChild(video)

  let hls: Hls | null = null
  let disposed = false

  const startPlayback = (): void => {
    if (disposed) return
    void video.play().catch(() => {
      // 自动播放被拦截时保持静音视频面（WebGL 中仍可显示黑帧/后续帧）
    })
  }

  try {
    if (video.canPlayType('application/vnd.apple.mpegurl') !== '') {
      video.src = url
      video.addEventListener('loadedmetadata', startPlayback, { once: true })
      startPlayback()
    } else {
      const module = await import('hls.js')
      const HlsCtor = module.default
      if (!HlsCtor.isSupported()) {
        video.remove()
        return null
      }
      if (disposed) {
        video.remove()
        return null
      }
      const instance = new HlsCtor({ enableWorker: true, lowLatencyMode: true, backBufferLength: 20 })
      hls = instance
      instance.on(HlsCtor.Events.MANIFEST_PARSED, startPlayback)
      instance.loadSource(url)
      instance.attachMedia(video)
    }
  } catch {
    hls?.destroy()
    video.remove()
    return null
  }

  if (disposed) {
    hls?.destroy()
    video.remove()
    return null
  }

  const texture = new THREE.VideoTexture(video)
  texture.colorSpace = THREE.SRGBColorSpace
  texture.minFilter = THREE.LinearFilter
  texture.magFilter = THREE.LinearFilter

  const distance = THREE.MathUtils.clamp(camera.range_m * FUSION_DISTANCE_RATIO, 20, 220)
  const fovH = THREE.MathUtils.degToRad(THREE.MathUtils.clamp(camera.fov_h, 5, 150))
  const fovV = camera.fov_v > 0
    ? THREE.MathUtils.degToRad(THREE.MathUtils.clamp(camera.fov_v, 5, 150))
    : 2 * Math.atan(Math.tan(fovH / 2) / (16 / 9))
  const width = Math.max(6, 2 * distance * Math.tan(fovH / 2))
  const height = Math.max(4, 2 * distance * Math.tan(fovV / 2))

  const group = new THREE.Group()
  group.name = 'video-fusion'
  group.position.copy(enuToThree(camera.x, camera.y, camera.z))

  const center = headingToThreeDirection(camera.heading).multiplyScalar(distance).add(group.position)
  const facing = group.position.clone()

  const planeGeometry = new THREE.PlaneGeometry(width, height)
  const planeMaterial = new THREE.MeshBasicMaterial({
    map: texture,
    transparent: true,
    opacity: 0.88,
    side: THREE.DoubleSide,
    depthWrite: false,
  })
  const plane = new THREE.Mesh(planeGeometry, planeMaterial)
  plane.position.copy(center)
  plane.lookAt(facing)
  plane.renderOrder = 3
  group.add(plane)

  // 边缘描边
  const frameGeometry = new THREE.EdgesGeometry(new THREE.BoxGeometry(width, height, 0.15))
  const frameMaterial = new THREE.LineBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.9 })
  const frame = new THREE.LineSegments(frameGeometry, frameMaterial)
  frame.position.copy(center)
  frame.quaternion.copy(plane.quaternion)
  group.add(frame)

  // 暗角（径向渐变贴图，位于视频面前方一点）
  const vignetteCanvas = document.createElement('canvas')
  vignetteCanvas.width = 256
  vignetteCanvas.height = 256
  const vignetteContext = vignetteCanvas.getContext('2d')
  if (vignetteContext) {
    const gradient = vignetteContext.createRadialGradient(128, 128, 40, 128, 128, 150)
    gradient.addColorStop(0, 'rgba(3, 8, 15, 0)')
    gradient.addColorStop(0.72, 'rgba(3, 8, 15, 0.18)')
    gradient.addColorStop(1, 'rgba(3, 8, 15, 0.72)')
    vignetteContext.fillStyle = gradient
    vignetteContext.fillRect(0, 0, 256, 256)
  }
  const vignetteTexture = new THREE.CanvasTexture(vignetteCanvas)
  vignetteTexture.colorSpace = THREE.SRGBColorSpace
  const vignetteMaterial = new THREE.MeshBasicMaterial({
    map: vignetteTexture,
    transparent: true,
    depthWrite: false,
    opacity: 0.95,
    side: THREE.DoubleSide,
  })
  const vignette = new THREE.Mesh(new THREE.PlaneGeometry(width, height), vignetteMaterial)
  const vignetteCenter = center.clone().add(facing.clone().sub(center).normalize().multiplyScalar(0.3))
  vignette.position.copy(vignetteCenter)
  vignette.quaternion.copy(plane.quaternion)
  vignette.renderOrder = 4
  group.add(vignette)

  // LIVE 标签
  const label = createTextSprite(`LIVE · ${camera.code}`, { worldHeight: Math.max(2.5, height * 0.07), color: '#bae6fd' })
  label.position.copy(center).add(new THREE.Vector3(0, height / 2 + 2.5, 0))
  group.add(label)

  return {
    group,
    dispose() {
      disposed = true
      hls?.destroy()
      hls = null
      video.pause()
      video.removeAttribute('src')
      video.load()
      video.remove()
      texture.dispose()
      group.parent?.remove(group)
      disposeObject3D(group)
    },
  }
}
