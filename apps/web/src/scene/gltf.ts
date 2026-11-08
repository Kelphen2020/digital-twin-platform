import * as THREE from 'three'
import type { GLTF } from 'three/examples/jsm/loaders/GLTFLoader.js'
import type { SiteModel } from '../types'
import { enuToThree } from './coordinates'

export interface LoadedSiteModel {
  id: string
  name: string
  object: THREE.Object3D
}

export interface SiteModelLoadResult {
  loaded: LoadedSiteModel[]
  failed: SiteModel[]
}

/**
 * 按 GET /api/site 的 models 清单加载 glTF/GLB（GLTFLoader 按需动态加载）。
 * 单个模型失败不影响其它模型；调用方据结果决定是否保留程序化建筑。
 */
export async function loadSiteModels(models: SiteModel[]): Promise<SiteModelLoadResult> {
  if (models.length === 0) return { loaded: [], failed: [] }

  const { GLTFLoader } = await import('three/examples/jsm/loaders/GLTFLoader.js')
  const loader = new GLTFLoader()
  const loaded: LoadedSiteModel[] = []
  const failed: SiteModel[] = []

  for (const model of models) {
    try {
      const gltf: GLTF = await loader.loadAsync(model.url)
      const object = gltf.scene
      object.name = model.id
      const transform = model.transform
      object.position.copy(enuToThree(transform?.x ?? 0, transform?.y ?? 0, transform?.z ?? 0))
      // rotation_y 按项目约定使用角度制
      object.rotation.y = THREE.MathUtils.degToRad(transform?.rotation_y ?? 0)
      const scale = typeof transform?.scale === 'number' && transform.scale > 0 ? transform.scale : 1
      object.scale.setScalar(scale)
      object.traverse((child) => {
        const mesh = child as THREE.Mesh
        if (mesh.isMesh) {
          mesh.castShadow = true
          mesh.receiveShadow = true
        }
      })
      loaded.push({ id: model.id, name: model.name || model.id, object })
    } catch {
      failed.push(model)
    }
  }

  return { loaded, failed }
}
