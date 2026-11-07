import * as THREE from 'three'

export interface TextSpriteOptions {
  color?: string
  background?: string
  fontSize?: number
  /** 世界坐标中的文本高度（米），用于控制 Sprite 屏占比。 */
  worldHeight?: number
}

/** 用 Canvas 生成文字贴图，作为建筑名称等场景标注。 */
export function createTextSprite(text: string, options: TextSpriteOptions = {}): THREE.Sprite {
  const fontSize = options.fontSize ?? 48
  const padding = 20
  const color = options.color ?? '#cfe6ff'
  const background = options.background ?? 'rgba(8, 20, 38, 0.72)'
  const worldHeight = options.worldHeight ?? 5

  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  if (!context) throw new Error('无法创建 Canvas 2D 上下文')

  const font = `600 ${fontSize}px "PingFang SC", "Microsoft YaHei", sans-serif`
  context.font = font
  const textWidth = Math.ceil(context.measureText(text).width)
  const width = textWidth + padding * 2
  const height = fontSize + padding * 2

  canvas.width = width
  canvas.height = height
  // 修改 canvas 尺寸会重置上下文，需要重新设置绘制属性
  context.font = font
  context.fillStyle = background
  context.fillRect(0, 0, width, height)
  context.strokeStyle = 'rgba(90, 160, 255, 0.55)'
  context.lineWidth = 3
  context.strokeRect(1.5, 1.5, width - 3, height - 3)
  context.fillStyle = color
  context.textAlign = 'center'
  context.textBaseline = 'middle'
  context.fillText(text, width / 2, height / 2 + 2)

  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace
  texture.minFilter = THREE.LinearFilter

  const material = new THREE.SpriteMaterial({ map: texture, transparent: true, depthWrite: false })
  const sprite = new THREE.Sprite(material)
  sprite.scale.set((worldHeight * width) / height, worldHeight, 1)
  return sprite
}
