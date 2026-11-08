# P6.0 API 扩展（三维站点配置）

在 P0–P5 契约之上追加。P6 的三维增强（2.5D 底图、LOD、glTF 模型、视频纹理融合）由前端实现，后端只提供**站点与模型清单**。

## `GET /api/site`（登录用户）

```json
{
  "name": "示范园区",
  "origin": { "lat": 30.2741, "lng": 120.1551, "alt": 0 },
  "extent": { "min_x": -200, "max_x": 200, "min_y": -200, "max_y": 200, "min_z": 0, "max_z": 60 },
  "models": [
    {
      "id": "site-overview",
      "name": "园区总览模型",
      "format": "gltf",
      "url": "/models/site.gltf",
      "transform": { "x": 0, "y": 0, "z": 0, "rotation_y": 0, "scale": 1 }
    }
  ],
  "map": { "style": "vector", "image_url": null }
}
```

- `url` 是前端静态资源路径（`public/models/...`）或绝对 URL；`format` 目前支持 `gltf`/`glb`。
- `extent` 为局部 ENU 米制范围，前端据此生成 2.5D 底图与地面网格。
- `map.image_url` 非空时前端可用正射影像作为底图；为 `null` 时使用矢量风格程序化底图。
- 配置来源：内置默认值；设置 `SITE_CONFIG_FILE=/path/site.json` 可覆盖（文件结构同上，缺省字段用默认值补齐）。

## 变更

- 无新增写接口；无新环境变量（除 `SITE_CONFIG_FILE`）。
- 前端新增工具栏开关：轨迹 / 视频融合 / 模型细节（LOD）/ 2.5D 底图。
- 视频纹理融合使用选中相机的 `stream_url`（HLS）在视锥内贴视频面，默认关闭。
