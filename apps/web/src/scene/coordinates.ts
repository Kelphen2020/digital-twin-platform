import * as THREE from 'three'

/** 场地范围：400 × 400 m 的局部 ENU 坐标系。 */
export const SITE_SIZE_M = 400

/**
 * ENU（x 东、y 北、z 上）→ Three.js（x 东、y 上、z 南）：
 * three.x = ENU.x，three.y = ENU.z，three.z = -ENU.y。
 */
export function enuToThree(x: number, y: number, z = 0): THREE.Vector3 {
  return new THREE.Vector3(x, z, -y)
}

/**
 * 相机方位角（度，正北 0、顺时针为正）→ Three.js 水平单位方向向量。
 * 正北对应 three -Z，正东对应 three +X。
 */
export function headingToThreeDirection(headingDeg: number): THREE.Vector3 {
  const rad = THREE.MathUtils.degToRad(headingDeg)
  return new THREE.Vector3(Math.sin(rad), 0, -Math.cos(rad))
}
