import * as THREE from 'three'
import { CAMERA_STATUS_COLORS } from '../labels'
import type { Camera } from '../types'
import { enuToThree, headingToThreeDirection } from './coordinates'
import { disposeObject3D } from './dispose'

export interface CameraMarker {
  cameraId: string
  group: THREE.Group
  /** 参与射线拾取的网格（本体/视锥/立杆）。 */
  interactive: THREE.Object3D[]
  dispose(): void
}

const BODY_RADIUS = 1.6
const FRUSTUM_MIN_LENGTH = 18
const FRUSTUM_MAX_LENGTH = 90

/**
 * 生成一台相机的场景标记：
 * - 发光球体表示相机本体（颜色随 online/offline/degraded 状态）；
 * - 半透明视锥按 heading 朝向、fov_h 张角、range_m 长度生成；
 * - 选中时本体放大并在地面加高亮环。
 */
export function createCameraMarker(camera: Camera, selected: boolean): CameraMarker {
  const color = CAMERA_STATUS_COLORS[camera.status]
  const group = new THREE.Group()
  group.position.copy(enuToThree(camera.x, camera.y, camera.z))
  group.userData = { kind: 'camera', cameraId: camera.id }

  const interactive: THREE.Object3D[] = []
  const tag = <T extends THREE.Object3D>(object: T): T => {
    object.userData = { ...object.userData, kind: 'camera', cameraId: camera.id }
    interactive.push(object)
    return object
  }

  // 立杆：从地面连到相机安装高度
  if (camera.z > 1) {
    const pole = new THREE.Mesh(
      new THREE.CylinderGeometry(0.22, 0.22, camera.z, 8),
      new THREE.MeshStandardMaterial({ color: 0x5b718c, roughness: 0.7, metalness: 0.4 }),
    )
    pole.position.y = -camera.z / 2
    group.add(pole)
    tag(pole)
  }

  // 相机本体
  const bodyMaterial = new THREE.MeshStandardMaterial({
    color,
    emissive: color,
    emissiveIntensity: selected ? 1.6 : 0.85,
    roughness: 0.35,
    metalness: 0.2,
  })
  const body = new THREE.Mesh(
    new THREE.SphereGeometry(selected ? BODY_RADIUS * 1.35 : BODY_RADIUS, 20, 14),
    bodyMaterial,
  )
  group.add(body)
  tag(body)

  // 视锥：ConeGeometry 默认轴向 +Y、顶点在 +Y 端；平移到顶点原点后旋转到 +Z，再对准 heading
  const length = THREE.MathUtils.clamp(camera.range_m, FRUSTUM_MIN_LENGTH, FRUSTUM_MAX_LENGTH)
  const halfAngle = THREE.MathUtils.degToRad(THREE.MathUtils.clamp(camera.fov_h, 10, 120) / 2)
  const radius = Math.max(2, length * Math.tan(halfAngle) * 0.55)
  const frustumGeometry = new THREE.ConeGeometry(radius, length, 28, 1, true)
  frustumGeometry.translate(0, -length / 2, 0)
  frustumGeometry.rotateX(-Math.PI / 2)

  const direction = headingToThreeDirection(camera.heading)
  const frustum = new THREE.Mesh(
    frustumGeometry,
    new THREE.MeshBasicMaterial({
      color,
      transparent: true,
      opacity: selected ? 0.3 : 0.18,
      side: THREE.DoubleSide,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    }),
  )
  frustum.quaternion.setFromUnitVectors(new THREE.Vector3(0, 0, 1), direction)
  group.add(frustum)
  tag(frustum)

  // 视锥边缘线：暗色场景中更易辨认朝向与范围
  const edges = new THREE.LineSegments(
    new THREE.EdgesGeometry(frustumGeometry, 25),
    new THREE.LineBasicMaterial({ color, transparent: true, opacity: selected ? 0.7 : 0.35 }),
  )
  edges.quaternion.copy(frustum.quaternion)
  group.add(edges)

  // 选中态：地面高亮环
  if (selected) {
    const ring = new THREE.Mesh(
      new THREE.RingGeometry(BODY_RADIUS * 2, BODY_RADIUS * 2.8, 36),
      new THREE.MeshBasicMaterial({
        color,
        transparent: true,
        opacity: 0.85,
        side: THREE.DoubleSide,
        depthWrite: false,
      }),
    )
    ring.rotation.x = -Math.PI / 2
    ring.position.y = 0.15 - camera.z
    group.add(ring)
  }

  return {
    cameraId: camera.id,
    group,
    interactive,
    dispose() {
      disposeObject3D(group)
    },
  }
}
