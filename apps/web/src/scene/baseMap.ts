import * as THREE from 'three'
import type { Area, Camera, SiteConfig } from '../types'
import { resolveFootprints } from './buildings'
import { getSiteSize } from './siteDefaults'

/** 底图 Canvas 分辨率（方形，映射到站点 extent）。 */
const MAP_SIZE = 2048

export interface BaseMapLayer {
  mesh: THREE.Mesh
  dispose(): void
}

const ZONE_STYLES: Record<string, { fill: string; stroke: string }> = {
  building: { fill: 'rgba(28, 58, 94, 0.9)', stroke: 'rgba(96, 165, 250, 0.8)' },
  zone: { fill: 'rgba(34, 197, 94, 0.10)', stroke: 'rgba(52, 211, 153, 0.55)' },
  road: { fill: 'rgba(56, 189, 248, 0.08)', stroke: 'rgba(56, 189, 248, 0.5)' },
  default: { fill: 'rgba(56, 189, 248, 0.08)', stroke: 'rgba(56, 189, 248, 0.45)' },
}

interface MapProjection {
  toX: (x: number) => number
  toY: (y: number) => number
  sx: number
  sy: number
}

/** 程序化 2.5D 矢量底图：区域/道路/建筑轮廓/相机点位/指北针/比例尺/扫描线。 */
export function createBaseMapLayer(site: SiteConfig, areas: Area[], cameras: Camera[]): BaseMapLayer {
  const { width, depth, centerX, centerY } = getSiteSize(site)
  const extent = site.extent
  const sx = MAP_SIZE / width
  const sy = MAP_SIZE / depth
  const projection: MapProjection = {
    toX: (x) => (x - extent.min_x) * sx,
    toY: (y) => (extent.max_y - y) * sy,
    sx,
    sy,
  }

  const canvas = document.createElement('canvas')
  canvas.width = MAP_SIZE
  canvas.height = MAP_SIZE
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('无法创建 2.5D 底图 Canvas 上下文')

  drawBackground(ctx)
  drawRoads(ctx, extent, projection)
  drawZones(ctx, areas, projection)
  drawFootprints(ctx, areas, projection)
  drawCameraMarkers(ctx, cameras, projection)
  drawAnnotations(ctx, site, projection)
  drawScanlines(ctx)
  drawVignette(ctx)

  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace
  texture.anisotropy = 4

  const geometry = new THREE.PlaneGeometry(width, depth)
  geometry.rotateX(-Math.PI / 2)
  const mesh = new THREE.Mesh(
    geometry,
    new THREE.MeshBasicMaterial({ map: texture, transparent: true, depthWrite: false, toneMapped: false }),
  )
  mesh.name = 'base-map'
  mesh.position.set(centerX, 0.05, -centerY)
  mesh.renderOrder = 2

  return {
    mesh,
    dispose() {
      mesh.parent?.remove(mesh)
      geometry.dispose()
      const material = mesh.material as THREE.MeshBasicMaterial
      material.map?.dispose()
      material.dispose()
      texture.dispose()
    },
  }
}

function drawBackground(ctx: CanvasRenderingContext2D): void {
  const gradient = ctx.createRadialGradient(MAP_SIZE / 2, MAP_SIZE / 2, MAP_SIZE * 0.1, MAP_SIZE / 2, MAP_SIZE / 2, MAP_SIZE * 0.72)
  gradient.addColorStop(0, '#0b1e34')
  gradient.addColorStop(1, '#050f1d')
  ctx.fillStyle = gradient
  ctx.fillRect(0, 0, MAP_SIZE, MAP_SIZE)

  // 底图网格（50 m）
  ctx.strokeStyle = 'rgba(56, 189, 248, 0.075)'
  ctx.lineWidth = 1
  const step = MAP_SIZE / 8
  for (let i = 1; i < 8; i += 1) {
    ctx.beginPath()
    ctx.moveTo(i * step, 0)
    ctx.lineTo(i * step, MAP_SIZE)
    ctx.stroke()
    ctx.beginPath()
    ctx.moveTo(0, i * step)
    ctx.lineTo(MAP_SIZE, i * step)
    ctx.stroke()
  }
}

function drawRoads(ctx: CanvasRenderingContext2D, extent: SiteConfig['extent'], projection: MapProjection): void {
  const width = extent.max_x - extent.min_x
  const depth = extent.max_y - extent.min_y
  const verticals = [extent.min_x + width * 0.26, extent.min_x + width * 0.74]
  const horizontals = [extent.min_y + depth * 0.3, extent.min_y + depth * 0.7]

  ctx.lineCap = 'round'
  for (const x of verticals) {
    const px = projection.toX(x)
    ctx.strokeStyle = 'rgba(14, 32, 54, 0.95)'
    ctx.lineWidth = 46
    ctx.beginPath()
    ctx.moveTo(px, 0)
    ctx.lineTo(px, MAP_SIZE)
    ctx.stroke()
    ctx.strokeStyle = 'rgba(45, 88, 133, 0.9)'
    ctx.lineWidth = 3
    ctx.setLineDash([26, 22])
    ctx.beginPath()
    ctx.moveTo(px, 0)
    ctx.lineTo(px, MAP_SIZE)
    ctx.stroke()
    ctx.setLineDash([])
  }
  for (const y of horizontals) {
    const py = projection.toY(y)
    ctx.strokeStyle = 'rgba(14, 32, 54, 0.95)'
    ctx.lineWidth = 40
    ctx.beginPath()
    ctx.moveTo(0, py)
    ctx.lineTo(MAP_SIZE, py)
    ctx.stroke()
    ctx.strokeStyle = 'rgba(45, 88, 133, 0.9)'
    ctx.lineWidth = 3
    ctx.setLineDash([26, 22])
    ctx.beginPath()
    ctx.moveTo(0, py)
    ctx.lineTo(MAP_SIZE, py)
    ctx.stroke()
    ctx.setLineDash([])
  }
}

function tracePolygon(ctx: CanvasRenderingContext2D, polygon: number[][], projection: MapProjection): void {
  polygon.forEach(([x, y], index) => {
    const px = projection.toX(x)
    const py = projection.toY(y)
    if (index === 0) ctx.moveTo(px, py)
    else ctx.lineTo(px, py)
  })
  ctx.closePath()
}

function drawZones(ctx: CanvasRenderingContext2D, areas: Area[], projection: MapProjection): void {
  for (const area of areas) {
    if (!area.polygon || area.polygon.length < 3) continue
    if (area.type === 'building') continue
    const style = ZONE_STYLES[area.type] ?? ZONE_STYLES.default
    ctx.beginPath()
    tracePolygon(ctx, area.polygon, projection)
    ctx.fillStyle = style.fill
    ctx.fill()
    ctx.strokeStyle = style.stroke
    ctx.lineWidth = 2.5
    ctx.stroke()

    const center = polygonCentroid(area.polygon)
    ctx.fillStyle = 'rgba(125, 211, 252, 0.85)'
    ctx.font = '600 26px "PingFang SC", "Microsoft YaHei", sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText(area.name, projection.toX(center[0]), projection.toY(center[1]))
  }
}

function drawFootprints(ctx: CanvasRenderingContext2D, areas: Area[], projection: MapProjection): void {
  for (const footprint of resolveFootprints(areas)) {
    ctx.beginPath()
    tracePolygon(ctx, footprint.polygon, projection)
    const style = ZONE_STYLES.building
    ctx.fillStyle = style.fill
    ctx.fill()
    ctx.strokeStyle = style.stroke
    ctx.lineWidth = 3.5
    ctx.stroke()

    // 屋顶内缩高亮，制造 2.5D 立体感
    const center = polygonCentroid(footprint.polygon)
    ctx.save()
    ctx.translate(projection.toX(center[0]), projection.toY(center[1]))
    ctx.globalAlpha = 0.22
    ctx.fillStyle = '#38bdf8'
    ctx.fillRect(-60, -34, 120, 68)
    ctx.restore()

    ctx.fillStyle = 'rgba(191, 219, 254, 0.9)'
    ctx.font = '600 24px "PingFang SC", "Microsoft YaHei", sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText(footprint.label, projection.toX(center[0]), projection.toY(center[1]) + 70)
  }
}

function drawCameraMarkers(ctx: CanvasRenderingContext2D, cameras: Camera[], projection: MapProjection): void {
  for (const camera of cameras) {
    const px = projection.toX(camera.x)
    const py = projection.toY(camera.y)
    const heading = THREE.MathUtils.degToRad(camera.heading)
    const dirX = Math.sin(heading)
    const dirY = -Math.cos(heading)

    // 视野扇形
    const fov = THREE.MathUtils.degToRad(THREE.MathUtils.clamp(camera.fov_h, 10, 120) / 2)
    const fanRadius = Math.min(160, Math.max(80, camera.range_m * 0.5)) * projection.sx * 0.5
    ctx.beginPath()
    ctx.moveTo(px, py)
    ctx.arc(px, py, fanRadius, Math.atan2(dirY, dirX) - fov, Math.atan2(dirY, dirX) + fov)
    ctx.closePath()
    ctx.fillStyle = 'rgba(56, 189, 248, 0.10)'
    ctx.fill()
    ctx.strokeStyle = 'rgba(56, 189, 248, 0.35)'
    ctx.lineWidth = 1.5
    ctx.stroke()

    // 相机点位
    ctx.beginPath()
    ctx.arc(px, py, 11, 0, Math.PI * 2)
    ctx.fillStyle = '#38bdf8'
    ctx.fill()
    ctx.strokeStyle = '#e0f2fe'
    ctx.lineWidth = 2.5
    ctx.stroke()

    ctx.fillStyle = 'rgba(186, 230, 253, 0.9)'
    ctx.font = '600 21px "SFMono-Regular", ui-monospace, monospace'
    ctx.textAlign = 'left'
    ctx.fillText(camera.code, px + 18, py - 12)
  }
}

function drawAnnotations(ctx: CanvasRenderingContext2D, site: SiteConfig, projection: MapProjection): void {
  // 标题
  ctx.fillStyle = 'rgba(224, 242, 254, 0.95)'
  ctx.font = '600 34px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.textAlign = 'left'
  ctx.fillText(`${site.name} · 2.5D BASE MAP`, 48, 66)
  ctx.fillStyle = 'rgba(125, 211, 252, 0.6)'
  ctx.font = '20px "SFMono-Regular", ui-monospace, monospace'
  ctx.fillText(
    `ENU ${site.extent.min_x}..${site.extent.max_x} / ${site.extent.min_y}..${site.extent.max_y} m · ${site.origin.lat.toFixed(4)}, ${site.origin.lng.toFixed(4)}`,
    48,
    100,
  )

  // 指北针（右上）
  const nx = MAP_SIZE - 96
  const ny = 104
  ctx.save()
  ctx.translate(nx, ny)
  ctx.beginPath()
  ctx.moveTo(0, -44)
  ctx.lineTo(22, 26)
  ctx.lineTo(0, 12)
  ctx.lineTo(-22, 26)
  ctx.closePath()
  ctx.fillStyle = 'rgba(56, 189, 248, 0.9)'
  ctx.fill()
  ctx.fillStyle = 'rgba(224, 242, 254, 0.95)'
  ctx.font = '700 26px "SFMono-Regular", ui-monospace, monospace'
  ctx.textAlign = 'center'
  ctx.fillText('N', 0, -58)
  ctx.restore()

  // 比例尺（左下，50 m）
  const meters = 50
  const barLength = meters * projection.sx
  const bx = 56
  const by = MAP_SIZE - 64
  ctx.strokeStyle = 'rgba(224, 242, 254, 0.9)'
  ctx.lineWidth = 3
  ctx.beginPath()
  ctx.moveTo(bx, by)
  ctx.lineTo(bx + barLength, by)
  ctx.moveTo(bx, by - 8)
  ctx.lineTo(bx, by + 8)
  ctx.moveTo(bx + barLength, by - 8)
  ctx.lineTo(bx + barLength, by + 8)
  ctx.stroke()
  ctx.fillStyle = 'rgba(224, 242, 254, 0.9)'
  ctx.font = '22px "SFMono-Regular", ui-monospace, monospace'
  ctx.textAlign = 'left'
  ctx.fillText(`${meters} m`, bx, by - 18)
}

function drawScanlines(ctx: CanvasRenderingContext2D): void {
  ctx.fillStyle = 'rgba(56, 189, 248, 0.028)'
  for (let y = 0; y < MAP_SIZE; y += 4) {
    ctx.fillRect(0, y, MAP_SIZE, 1)
  }
}

function drawVignette(ctx: CanvasRenderingContext2D): void {
  const gradient = ctx.createRadialGradient(MAP_SIZE / 2, MAP_SIZE / 2, MAP_SIZE * 0.32, MAP_SIZE / 2, MAP_SIZE / 2, MAP_SIZE * 0.75)
  gradient.addColorStop(0, 'rgba(2, 8, 20, 0)')
  gradient.addColorStop(1, 'rgba(2, 8, 20, 0.62)')
  ctx.fillStyle = gradient
  ctx.fillRect(0, 0, MAP_SIZE, MAP_SIZE)
}

function polygonCentroid(polygon: number[][]): [number, number] {
  let x = 0
  let y = 0
  for (const point of polygon) {
    x += point[0]
    y += point[1]
  }
  return [x / polygon.length, y / polygon.length]
}
