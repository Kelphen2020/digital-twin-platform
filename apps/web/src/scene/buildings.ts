import * as THREE from 'three'
import type { Area } from '../types'
import { enuToThree } from './coordinates'
import { disposeObject3D } from './dispose'
import { createTextSprite } from './textSprite'

export interface BuildingSpec {
  label: string
  /** ENU 中心 (x, y)。 */
  center: [number, number]
  /** ENU 尺寸 (x 向, y 向, z 高)。 */
  size: [number, number, number]
  color: number
}

/** P0 固定布局：两栋演示建筑（glTF 模型缺失时的程序化回退）。 */
export const BUILDINGS: BuildingSpec[] = [
  { label: '1 号楼 · 60×40×20 m', center: [-40, 40], size: [60, 40, 20], color: 0x1c3a5e },
  { label: '2 号楼 · 50×30×14 m', center: [60, -20], size: [50, 30, 14], color: 0x193550 },
]

export type BuildingMode = 'lod' | 'simple'

export interface BuildingLayer {
  group: THREE.Group
  dispose(): void
}

interface BuildingMaterials {
  body: THREE.MeshStandardMaterial
  edge: THREE.LineBasicMaterial
  floor: THREE.LineBasicMaterial
  roof: THREE.MeshStandardMaterial
}

function createMaterials(color: number): BuildingMaterials {
  return {
    body: new THREE.MeshStandardMaterial({ color, roughness: 0.58, metalness: 0.35 }),
    edge: new THREE.LineBasicMaterial({ color: 0x4c8dff, transparent: true, opacity: 0.45 }),
    floor: new THREE.LineBasicMaterial({ color: 0x38bdf8, transparent: true, opacity: 0.2 }),
    roof: new THREE.MeshStandardMaterial({ color: 0x275483, emissive: 0x1d5f8a, emissiveIntensity: 0.25, roughness: 0.5 }),
  }
}

/** 近景细节：楼体 + 屋顶 + 楼层线 + 轮廓。 */
function createDetailedBuilding(building: BuildingSpec, materials: BuildingMaterials): THREE.Group {
  const [sizeX, sizeY, sizeZ] = building.size
  const group = new THREE.Group()

  const bodyGeometry = new THREE.BoxGeometry(sizeX, sizeZ, sizeY)
  const body = new THREE.Mesh(bodyGeometry, materials.body)
  body.position.y = sizeZ / 2
  body.castShadow = true
  body.receiveShadow = true
  group.add(body)

  const roof = new THREE.Mesh(new THREE.BoxGeometry(sizeX * 1.03, 0.9, sizeY * 1.03), materials.roof)
  roof.position.y = sizeZ + 0.45
  roof.castShadow = true
  group.add(roof)

  const edges = new THREE.LineSegments(new THREE.EdgesGeometry(bodyGeometry), materials.edge)
  edges.position.copy(body.position)
  group.add(edges)

  const floors = Math.max(2, Math.min(8, Math.round(sizeZ / 4.5)))
  const floorPoints: number[] = []
  for (let floor = 1; floor < floors; floor += 1) {
    const y = (sizeZ / floors) * floor
    const halfX = sizeX / 2 + 0.05
    const halfY = sizeY / 2 + 0.05
    floorPoints.push(
      -halfX, y, -halfY, halfX, y, -halfY,
      halfX, y, -halfY, halfX, y, halfY,
      halfX, y, halfY, -halfX, y, halfY,
      -halfX, y, halfY, -halfX, y, -halfY,
    )
  }
  const floorGeometry = new THREE.BufferGeometry()
  floorGeometry.setAttribute('position', new THREE.Float32BufferAttribute(floorPoints, 3))
  group.add(new THREE.LineSegments(floorGeometry, materials.floor))

  return group
}

/** 中景：简化盒体 + 轮廓。 */
function createSimplifiedBuilding(building: BuildingSpec, materials: BuildingMaterials): THREE.Group {
  const [sizeX, sizeY, sizeZ] = building.size
  const group = new THREE.Group()
  const bodyGeometry = new THREE.BoxGeometry(sizeX, sizeZ, sizeY)
  const body = new THREE.Mesh(bodyGeometry, materials.body)
  body.position.y = sizeZ / 2
  body.castShadow = true
  body.receiveShadow = true
  group.add(body)
  const edges = new THREE.LineSegments(new THREE.EdgesGeometry(bodyGeometry), materials.edge)
  edges.position.copy(body.position)
  group.add(edges)
  return group
}

/** 远景：地面轮廓 + 描边。 */
function createFootprint(building: BuildingSpec): THREE.Group {
  const [sizeX, sizeY] = building.size
  const group = new THREE.Group()
  const geometry = new THREE.PlaneGeometry(sizeX, sizeY)
  geometry.rotateX(-Math.PI / 2)
  const fill = new THREE.Mesh(
    geometry,
    new THREE.MeshBasicMaterial({ color: 0x1b3a5a, transparent: true, opacity: 0.55, depthWrite: false }),
  )
  fill.position.y = 0.18
  group.add(fill)

  const halfX = sizeX / 2
  const halfY = sizeY / 2
  const outlineGeometry = new THREE.BufferGeometry()
  outlineGeometry.setAttribute(
    'position',
    new THREE.Float32BufferAttribute(
      [-halfX, 0.22, -halfY, halfX, 0.22, -halfY, halfX, 0.22, -halfY, halfX, 0.22, halfY, halfX, 0.22, halfY, -halfX, 0.22, halfY, -halfX, 0.22, halfY, -halfX, 0.22, -halfY],
      3,
    ),
  )
  group.add(
    new THREE.LineSegments(
      outlineGeometry,
      new THREE.LineBasicMaterial({ color: 0x4c8dff, transparent: true, opacity: 0.75 }),
    ),
  )
  return group
}

/**
 * 程序化建筑层：
 * - lod：每栋建筑 THREE.LOD（近细节 / 中盒体 / 远轮廓）；
 * - simple：简单盒体；
 * - 若提供 gltfRoot（站点模型加载成功），LOD 用 glTF 作为近景，中/远景仍用程序化盒体与轮廓。
 */
export function createBuildingLayer(mode: BuildingMode, gltfRoot: THREE.Object3D | null): BuildingLayer {
  const group = new THREE.Group()
  group.name = 'buildings'
  const disposable: THREE.Object3D[] = []

  if (mode === 'lod') {
    if (gltfRoot) {
      const midGroup = new THREE.Group()
      const farGroup = new THREE.Group()
      for (const building of BUILDINGS) {
        const materials = createMaterials(building.color)
        const mid = createSimplifiedBuilding(building, materials)
        mid.position.copy(enuToThree(building.center[0], building.center[1], 0))
        midGroup.add(mid)
        const far = createFootprint(building)
        far.position.copy(enuToThree(building.center[0], building.center[1], 0))
        farGroup.add(far)
      }
      const lod = new THREE.LOD()
      lod.addLevel(gltfRoot, 0)
      lod.addLevel(midGroup, 520)
      lod.addLevel(farGroup, 760)
      group.add(lod)
      disposable.push(midGroup, farGroup)
    } else {
      for (const building of BUILDINGS) {
        const materials = createMaterials(building.color)
        const lod = new THREE.LOD()
        lod.addLevel(createDetailedBuilding(building, materials), 0)
        lod.addLevel(createSimplifiedBuilding(building, materials), 180)
        lod.addLevel(createFootprint(building), 380)
        lod.position.copy(enuToThree(building.center[0], building.center[1], 0))
        group.add(lod)
        disposable.push(lod)
      }
    }
  } else {
    for (const building of BUILDINGS) {
      const materials = createMaterials(building.color)
      const simple = createSimplifiedBuilding(building, materials)
      simple.position.copy(enuToThree(building.center[0], building.center[1], 0))
      group.add(simple)
      disposable.push(simple)
    }
  }

  // 建筑名称标签（LOD 切换时始终可见）
  for (const building of BUILDINGS) {
    const label = createTextSprite(building.label, { worldHeight: 5.5 })
    label.position.copy(enuToThree(building.center[0], building.center[1], building.size[2] + 9))
    group.add(label)
    disposable.push(label)
  }

  return {
    group,
    dispose() {
      // glTF 模型由 TwinScene 持有；这里仅从 LOD 中摘除，不释放其资源
      if (gltfRoot && gltfRoot.parent) gltfRoot.parent.remove(gltfRoot)
      for (const object of disposable) {
        object.parent?.remove(object)
        disposeObject3D(object)
      }
      disposable.length = 0
      group.clear()
    },
  }
}

/** 底图所需的建筑轮廓：优先使用 /api/areas 中 building 类型多边形，否则回退到固定布局。 */
export function resolveFootprints(areas: Area[]): { polygon: number[][]; label: string }[] {
  const buildingAreas = areas.filter((area) => area.type === 'building' && Array.isArray(area.polygon) && area.polygon.length >= 3)
  if (buildingAreas.length > 0) {
    return buildingAreas.map((area) => ({ polygon: area.polygon as number[][], label: area.name }))
  }
  return BUILDINGS.map((building) => {
    const [sizeX, sizeY] = building.size
    const [cx, cy] = building.center
    return {
      label: building.label.split(' ')[0],
      polygon: [
        [cx - sizeX / 2, cy - sizeY / 2],
        [cx + sizeX / 2, cy - sizeY / 2],
        [cx + sizeX / 2, cy + sizeY / 2],
        [cx - sizeX / 2, cy + sizeY / 2],
      ],
    }
  })
}
