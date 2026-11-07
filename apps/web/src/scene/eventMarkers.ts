import * as THREE from 'three'
import { SEVERITY_COLORS } from '../labels'
import type { TwinEvent } from '../types'
import { enuToThree } from './coordinates'
import { disposeObject3D } from './dispose'

/** 事件标记在场景中的可见时长；到期后仅从三维场景移除，事件流仍保留。 */
export const EVENT_MARKER_TTL_MS = 60_000

const PULSE_PERIOD_MS = 1800
const FADE_OUT_MS = 5000
const BEAM_HEIGHT = 9

export interface EventMarker {
  eventId: string
  group: THREE.Group
  /** 参与射线拾取的网格（光环/光柱/顶部标记体）。 */
  interactive: THREE.Object3D[]
  /** 事件发生时间（毫秒），用于 60s 自动过期与脉冲相位。 */
  occurredAt: number
  animate(now: number): void
  setSelected(selected: boolean): void
  dispose(): void
}

/**
 * 在事件世界坐标生成脉冲标记：
 * 地面基准环 + 扩散脉冲环 + 竖直光柱 + 顶部发光体，颜色按严重程度区分。
 * 返回 null 表示事件已超过 60s，不需要上图。
 */
export function createEventMarker(event: TwinEvent, now = Date.now()): EventMarker | null {
  const parsedTime = Date.parse(event.occurred_at)
  const occurredAt = Number.isFinite(parsedTime) ? parsedTime : now
  if (now - occurredAt > EVENT_MARKER_TTL_MS) return null

  const color = SEVERITY_COLORS[event.severity]
  const group = new THREE.Group()
  group.position.copy(enuToThree(event.x, event.y, event.z))
  group.userData = { kind: 'event', eventId: event.id }

  const interactive: THREE.Object3D[] = []
  const tag = <T extends THREE.Object3D>(object: T): T => {
    object.userData = { ...object.userData, kind: 'event', eventId: event.id }
    interactive.push(object)
    return object
  }

  // 地面高度（事件坐标 z 通常为 0；标记整体跟随事件坐标）
  const groundY = 0.12 - event.z

  const ringMaterial = new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity: 0.85,
    side: THREE.DoubleSide,
    depthWrite: false,
  })
  const ring = new THREE.Mesh(new THREE.RingGeometry(2.4, 3.2, 40), ringMaterial)
  ring.rotation.x = -Math.PI / 2
  ring.position.y = groundY
  group.add(ring)
  tag(ring)

  const pulseMaterial = new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity: 0.6,
    side: THREE.DoubleSide,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  const pulse = new THREE.Mesh(new THREE.RingGeometry(2.4, 3.2, 40), pulseMaterial)
  pulse.rotation.x = -Math.PI / 2
  pulse.position.y = groundY + 0.02
  group.add(pulse)
  tag(pulse)

  const beamMaterial = new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity: 0.5,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  const beam = new THREE.Mesh(new THREE.CylinderGeometry(0.32, 0.32, BEAM_HEIGHT, 12, 1, true), beamMaterial)
  beam.position.y = BEAM_HEIGHT / 2
  group.add(beam)
  tag(beam)

  const coreMaterial = new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity: 0.95,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  const core = new THREE.Mesh(new THREE.OctahedronGeometry(1.6), coreMaterial)
  core.position.y = BEAM_HEIGHT + 0.6
  group.add(core)
  tag(core)

  // 不可见的拾取代理：扩大标记的点击/悬停命中范围，提升小目标可操作性
  const hitProxy = new THREE.Mesh(
    new THREE.SphereGeometry(4.2, 10, 8),
    new THREE.MeshBasicMaterial({ transparent: true, opacity: 0, depthWrite: false }),
  )
  hitProxy.position.y = BEAM_HEIGHT / 2
  group.add(hitProxy)
  tag(hitProxy)

  let selected = false

  function applySelected(): void {
    ring.scale.setScalar(selected ? 1.45 : 1)
    core.scale.setScalar(selected ? 1.4 : 1)
  }

  applySelected()

  return {
    eventId: event.id,
    group,
    interactive,
    occurredAt,
    setSelected(next: boolean) {
      selected = next
      applySelected()
    },
    animate(nowMs: number) {
      const age = nowMs - occurredAt
      // 事件时间晚于本机时钟时 age 为负，用取模得到合法相位
      const phase = (((age % PULSE_PERIOD_MS) + PULSE_PERIOD_MS) % PULSE_PERIOD_MS) / PULSE_PERIOD_MS
      pulse.scale.setScalar(0.7 + phase * 2.8)

      // 到期前 5s 整体淡出
      const fade = age >= EVENT_MARKER_TTL_MS ? 0 : Math.min(1, (EVENT_MARKER_TTL_MS - age) / FADE_OUT_MS)
      pulseMaterial.opacity = (1 - phase) * 0.6 * fade
      ringMaterial.opacity = (selected ? 1 : 0.85) * fade
      beamMaterial.opacity = (selected ? 0.8 : 0.5) * fade
      coreMaterial.opacity = (selected ? 1 : 0.95) * fade
      core.position.y = BEAM_HEIGHT + 0.6 + Math.sin(nowMs / 260) * 0.7
    },
    dispose() {
      disposeObject3D(group)
    },
  }
}
