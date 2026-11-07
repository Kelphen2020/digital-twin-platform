import * as THREE from 'three'
import { SITE_SIZE_M, enuToThree } from './coordinates'
import { disposeObject3D } from './dispose'
import { createTextSprite } from './textSprite'

export interface SceneEnvironment {
  group: THREE.Group
  dispose(): void
}

interface BuildingSpec {
  label: string
  /** ENU 中心 (x, y)。 */
  center: [number, number]
  /** ENU 尺寸 (x 向, y 向, z 高)。 */
  size: [number, number, number]
  color: number
}

/** P0 固定布局：两栋演示建筑，坐标与 P0 需求一致。 */
const BUILDINGS: BuildingSpec[] = [
  { label: 'A 栋 · 60×40×20 m', center: [-40, 40], size: [60, 40, 20], color: 0x1c3a5e },
  { label: 'B 栋 · 50×30×14 m', center: [60, -20], size: [50, 30, 14], color: 0x193550 },
]

const GROUND_COLOR = 0x0a1a2c
const GRID_LINE_COLOR = 0x1e3a5f
const GRID_CENTER_COLOR = 0x3b82f6

/** 构建静态场景：天空、地面网格、两栋建筑、灯光。 */
export function createEnvironment(): SceneEnvironment {
  const group = new THREE.Group()
  group.name = 'environment'

  group.add(createSky())
  group.add(createGround())
  group.add(createGrid())
  for (const building of BUILDINGS) group.add(createBuilding(building))
  group.add(createLights())

  return {
    group,
    dispose() {
      disposeObject3D(group)
    },
  }
}

/** 渐变天穹；不受雾影响，提供深蓝夜空背景。 */
function createSky(): THREE.Mesh {
  const geometry = new THREE.SphereGeometry(1200, 32, 16)
  const material = new THREE.ShaderMaterial({
    side: THREE.BackSide,
    depthWrite: false,
    uniforms: {
      topColor: { value: new THREE.Color('#03080f') },
      horizonColor: { value: new THREE.Color('#0d2440') },
    },
    vertexShader: `
      varying vec3 vWorldPosition;
      void main() {
        vWorldPosition = (modelMatrix * vec4(position, 1.0)).xyz;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: `
      uniform vec3 topColor;
      uniform vec3 horizonColor;
      varying vec3 vWorldPosition;
      void main() {
        float height = normalize(vWorldPosition).y;
        float mixFactor = smoothstep(-0.05, 0.55, height);
        gl_FragColor = vec4(mix(horizonColor, topColor, mixFactor), 1.0);
      }
    `,
  })
  const sky = new THREE.Mesh(geometry, material)
  sky.renderOrder = -1
  return sky
}

/** 400×400 m 主地面 + 外围大地面（与雾融合，消除边界硬切）。 */
function createGround(): THREE.Group {
  const group = new THREE.Group()

  const geometry = new THREE.PlaneGeometry(SITE_SIZE_M, SITE_SIZE_M)
  geometry.rotateX(-Math.PI / 2)
  const ground = new THREE.Mesh(
    geometry,
    new THREE.MeshStandardMaterial({ color: GROUND_COLOR, roughness: 0.95, metalness: 0.15 }),
  )
  ground.receiveShadow = true
  group.add(ground)

  const apronGeometry = new THREE.PlaneGeometry(2400, 2400)
  apronGeometry.rotateX(-Math.PI / 2)
  const apron = new THREE.Mesh(
    apronGeometry,
    new THREE.MeshStandardMaterial({ color: 0x061020, roughness: 1, metalness: 0 }),
  )
  apron.position.y = -0.3
  group.add(apron)

  return group
}

/** 400 × 400 m、10 m 间距的地面网格。 */
function createGrid(): THREE.GridHelper {
  const grid = new THREE.GridHelper(SITE_SIZE_M, 40, GRID_CENTER_COLOR, GRID_LINE_COLOR)
  grid.position.y = 0.02
  const material = grid.material as THREE.LineBasicMaterial
  material.transparent = true
  material.opacity = 0.55
  material.depthWrite = false
  return grid
}

/** 按 ENU 中心与尺寸生成建筑盒体 + 轮廓线 + 名称标注。 */
function createBuilding(spec: BuildingSpec): THREE.Group {
  const [centerX, centerY] = spec.center
  const [sizeX, sizeY, sizeZ] = spec.size

  const group = new THREE.Group()
  group.position.copy(enuToThree(centerX, centerY, 0))

  const geometry = new THREE.BoxGeometry(sizeX, sizeZ, sizeY)
  const body = new THREE.Mesh(
    geometry,
    new THREE.MeshStandardMaterial({ color: spec.color, roughness: 0.58, metalness: 0.35 }),
  )
  body.position.y = sizeZ / 2
  body.castShadow = true
  body.receiveShadow = true
  group.add(body)

  const edges = new THREE.LineSegments(
    new THREE.EdgesGeometry(geometry),
    new THREE.LineBasicMaterial({ color: 0x4c8dff, transparent: true, opacity: 0.4 }),
  )
  edges.position.copy(body.position)
  group.add(edges)

  const label = createTextSprite(spec.label, { worldHeight: 5.5 })
  label.position.set(0, sizeZ + 9, 0)
  group.add(label)

  return group
}

/** 环境光 + 主平行光（带阴影）+ 冷色补光。 */
function createLights(): THREE.Group {
  const group = new THREE.Group()

  group.add(new THREE.AmbientLight(0x223a5e, 0.9))
  group.add(new THREE.HemisphereLight(0x9ec8ff, 0x050b16, 0.9))

  const keyLight = new THREE.DirectionalLight(0xe8f2ff, 2.2)
  keyLight.position.set(140, 200, 110)
  keyLight.castShadow = true
  keyLight.shadow.mapSize.set(2048, 2048)
  keyLight.shadow.camera.near = 10
  keyLight.shadow.camera.far = 700
  keyLight.shadow.camera.left = -240
  keyLight.shadow.camera.right = 240
  keyLight.shadow.camera.top = 240
  keyLight.shadow.camera.bottom = -240
  keyLight.shadow.bias = -0.0004
  group.add(keyLight)

  const fillLight = new THREE.DirectionalLight(0x3b82f6, 0.55)
  fillLight.position.set(-160, 120, -140)
  group.add(fillLight)

  return group
}
