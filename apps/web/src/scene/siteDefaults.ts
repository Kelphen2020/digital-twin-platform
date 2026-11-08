import type { SiteConfig } from '../types'

/** `/api/site` 不可用时的兜底站点（与 P0 演示场地一致）。 */
export const DEFAULT_SITE: SiteConfig = {
  name: '示范园区',
  origin: { lat: 0, lng: 0, alt: 0 },
  extent: { min_x: -200, max_x: 200, min_y: -200, max_y: 200, min_z: 0, max_z: 60 },
  models: [],
  map: { style: 'vector', image_url: null },
}

export interface SiteSize {
  width: number
  depth: number
  centerX: number
  centerY: number
}

/** 站点 ENU 范围的宽/深与中心。 */
export function getSiteSize(site: SiteConfig = DEFAULT_SITE): SiteSize {
  const { min_x, max_x, min_y, max_y } = site.extent
  return {
    width: Math.max(1, max_x - min_x),
    depth: Math.max(1, max_y - min_y),
    centerX: (min_x + max_x) / 2,
    centerY: (min_y + max_y) / 2,
  }
}
