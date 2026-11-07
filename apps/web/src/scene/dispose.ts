import * as THREE from 'three'

/**
 * 递归释放对象树中的几何体、材质与贴图，避免数据切换/组件卸载时显存泄漏。
 * 注意：three 的 Sprite 几何体是模块内共享的单例，不能在这里释放。
 */
export function disposeObject3D(root: THREE.Object3D): void {
  root.traverse((child) => {
    const mesh = child as THREE.Mesh
    const isSprite = (child as unknown as { isSprite?: boolean }).isSprite === true
    if (mesh.geometry && !isSprite) {
      mesh.geometry.dispose()
    }

    const material = mesh.material
    if (!material) return
    const materials = Array.isArray(material) ? material : [material]
    for (const item of materials) {
      const textured = item as THREE.MeshStandardMaterial
      if (textured.map) textured.map.dispose()
      item.dispose()
    }
  })
}
