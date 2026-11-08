import * as THREE from 'three'
import type { SiteConfig } from '../types'
import { disposeObject3D } from './dispose'
import { getSiteSize } from './siteDefaults'

export interface SceneEnvironment {
  group: THREE.Group
  dispose(): void
}

const GROUND_COLOR = 0x0a1a2c
const GRID_LINE_COLOR = 0x1e3a5f
const GRID_CENTER_COLOR = 0x3b82f6

/** 构建静态场景：天空、地面、网格、灯光（范围由站点 extent 决定）。 */
export function createEnvironment(site: SiteConfig): SceneEnvironment {
  const group = new THREE.Group()
  group.name = 'environment'

  const { width, depth, centerX, centerY } = getSiteSize(site)

  group.add(createSky())
  group.add(createGround(width, depth, centerX, centerY))
  group.add(createGrid(width, depth, centerX, centerY))
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
  const geometry = new THREE.SphereGeometry(1600, 32, 16)
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

/** 站点地面 + 外围大地面（与雾融合，消除边界硬切）。 */
function createGround(width: number, depth: number, centerX: number, centerY: number): THREE.Group {
  const group = new THREE.Group()

  const geometry = new THREE.PlaneGeometry(width, depth)
  geometry.rotateX(-Math.PI / 2)
  const ground = new THREE.Mesh(
    geometry,
    new THREE.MeshStandardMaterial({ color: GROUND_COLOR, roughness: 0.95, metalness: 0.15 }),
  )
  ground.position.set(centerX, 0, -centerY)
  ground.receiveShadow = true
  group.add(ground)

  const apronSize = Math.max(2400, Math.max(width, depth) * 3)
  const apronGeometry = new THREE.PlaneGeometry(apronSize, apronSize)
  apronGeometry.rotateX(-Math.PI / 2)
  const apron = new THREE.Mesh(
    apronGeometry,
    new THREE.MeshStandardMaterial({ color: 0x061020, roughness: 1, metalness: 0 }),
  )
  apron.position.set(centerX, -0.3, -centerY)
  group.add(apron)

  return group
}

/** 与 extent 匹配、10 m 间距的地面网格。 */
function createGrid(width: number, depth: number, centerX: number, centerY: number): THREE.GridHelper {
  const gridSize = Math.max(width, depth)
  const divisions = Math.max(2, Math.round(gridSize / 10))
  const grid = new THREE.GridHelper(gridSize, divisions, GRID_CENTER_COLOR, GRID_LINE_COLOR)
  grid.position.set(centerX, 0.02, -centerY)
  const material = grid.material as THREE.LineBasicMaterial
  material.transparent = true
  material.opacity = 0.55
  material.depthWrite = false
  return grid
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
