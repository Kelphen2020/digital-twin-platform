import * as THREE from 'three'
import { TRACK_CLASS_COLORS } from '../labels'
import type { TrackPoint } from '../types'
import { enuToThree } from './coordinates'

/** 单条轨迹最多绘制点数 / 场景内最多轨迹数。 */
const MAX_POINTS_PER_TRACK = 300
const MAX_TRACKS = 40
/** 轨迹淡出时长：超过该时长的点接近背景色。 */
const FADE_MS = 180_000
/** 轨迹离地高度，避免与地面网格 z-fighting。 */
const TRACK_Y = 0.4

const BACKGROUND_COLOR = new THREE.Color(0x0a1a2c)

interface TrackLine {
  line: THREE.Line
  geometry: THREE.BufferGeometry
  material: THREE.LineBasicMaterial
  color: THREE.Color
}

export interface TrackLineLayer {
  group: THREE.Group
  /** 用当前轨迹点集合刷新折线（按时间淡出旧点）。 */
  update(points: TrackPoint[], now: number): void
  dispose(): void
}

/** 创建相机轨迹图层：每条轨迹一条带顶点颜色的 Line，老点向背景色淡出。 */
export function createTrackLineLayer(): TrackLineLayer {
  const group = new THREE.Group()
  group.name = 'track-lines'
  const lines = new Map<string, TrackLine>()

  function createLine(colorHex: string): TrackLine {
    const geometry = new THREE.BufferGeometry()
    geometry.setAttribute('position', new THREE.BufferAttribute(new Float32Array(0), 3))
    geometry.setAttribute('color', new THREE.BufferAttribute(new Float32Array(0), 3))
    const material = new THREE.LineBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: 0.95,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    })
    const line = new THREE.Line(geometry, material)
    line.frustumCulled = false
    return { line, geometry, material, color: new THREE.Color(colorHex) }
  }

  function update(points: TrackPoint[], now: number): void {
    const grouped = new Map<string, TrackPoint[]>()
    for (const point of points) {
      const bucket = grouped.get(point.track_id)
      if (bucket) bucket.push(point)
      else grouped.set(point.track_id, [point])
    }

    // 轨迹过多时只保留最近更新的 MAX_TRACKS 条
    let entries = Array.from(grouped.entries())
    if (entries.length > MAX_TRACKS) {
      entries = entries
        .sort((a, b) => latestTs(b[1]) - latestTs(a[1]))
        .slice(0, MAX_TRACKS)
    }

    const seen = new Set<string>()
    for (const [trackId, trackPoints] of entries) {
      seen.add(trackId)
      const sorted = trackPoints
        .slice()
        .sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts))
        .slice(-MAX_POINTS_PER_TRACK)
      if (sorted.length === 0) continue

      const colorHex = TRACK_CLASS_COLORS[sorted[sorted.length - 1].class] ?? '#94a3b8'
      let trackLine = lines.get(trackId)
      if (!trackLine) {
        trackLine = createLine(colorHex)
        lines.set(trackId, trackLine)
        group.add(trackLine.line)
      }
      trackLine.color.set(colorHex)

      const positions = new Float32Array(sorted.length * 3)
      const colors = new Float32Array(sorted.length * 3)
      for (let index = 0; index < sorted.length; index += 1) {
        const point = sorted[index]
        const position = enuToThree(point.x, point.y, TRACK_Y)
        positions[index * 3] = position.x
        positions[index * 3 + 1] = position.y
        positions[index * 3 + 2] = position.z

        // 老点向背景色淡出：age=0 时为原色，age>=FADE_MS 时仅保留 15% 亮度
        const age = Math.max(0, now - Date.parse(point.ts))
        const freshness = Math.max(0, Math.min(1, 1 - age / FADE_MS))
        const brightness = 0.15 + 0.85 * freshness
        const faded = trackLine.color.clone().lerp(BACKGROUND_COLOR, 1 - brightness)
        colors[index * 3] = faded.r
        colors[index * 3 + 1] = faded.g
        colors[index * 3 + 2] = faded.b
      }

      trackLine.geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3))
      trackLine.geometry.setAttribute('color', new THREE.BufferAttribute(colors, 3))
      trackLine.geometry.setDrawRange(0, sorted.length)
      trackLine.geometry.computeBoundingSphere()
      trackLine.line.visible = sorted.length >= 2
    }

    // 清理已不存在的轨迹
    for (const [trackId, trackLine] of lines) {
      if (seen.has(trackId)) continue
      group.remove(trackLine.line)
      trackLine.geometry.dispose()
      trackLine.material.dispose()
      lines.delete(trackId)
    }
  }

  return {
    group,
    update,
    dispose() {
      for (const trackLine of lines.values()) {
        trackLine.geometry.dispose()
        trackLine.material.dispose()
      }
      lines.clear()
      group.clear()
    },
  }
}

function latestTs(points: TrackPoint[]): number {
  let latest = Number.NEGATIVE_INFINITY
  for (const point of points) {
    const ts = Date.parse(point.ts)
    if (Number.isFinite(ts) && ts > latest) latest = ts
  }
  return latest
}
