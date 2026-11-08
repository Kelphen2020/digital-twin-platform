#!/usr/bin/env python3
"""Generate a minimal glTF 2.0 site model for the P6.0 demo.

Three box buildings with per-node translation/scale are enough to prove the
GLTFLoader/LOD pipeline without shipping a DCC asset. Coordinates follow the
frontend mapping three.x = ENU.x, three.y = ENU.z, three.z = -ENU.y.

Usage:
    python3 tools/make_site_model.py [output.gltf]
Default output: apps/web/public/models/site.gltf
"""
from __future__ import annotations

import base64
import json
import struct
import sys
from pathlib import Path

# Unit cube centered at the origin (side length 1), 24 vertices with normals.
FACES = [
    ((0, 0, 1), [(-0.5, -0.5, 0.5), (0.5, -0.5, 0.5), (0.5, 0.5, 0.5), (-0.5, 0.5, 0.5)]),
    ((0, 0, -1), [(0.5, -0.5, -0.5), (-0.5, -0.5, -0.5), (-0.5, 0.5, -0.5), (0.5, 0.5, -0.5)]),
    ((1, 0, 0), [(0.5, -0.5, 0.5), (0.5, -0.5, -0.5), (0.5, 0.5, -0.5), (0.5, 0.5, 0.5)]),
    ((-1, 0, 0), [(-0.5, -0.5, -0.5), (-0.5, -0.5, 0.5), (-0.5, 0.5, 0.5), (-0.5, 0.5, -0.5)]),
    ((0, 1, 0), [(-0.5, 0.5, 0.5), (0.5, 0.5, 0.5), (0.5, 0.5, -0.5), (-0.5, 0.5, -0.5)]),
    ((0, -1, 0), [(-0.5, -0.5, -0.5), (0.5, -0.5, -0.5), (0.5, -0.5, 0.5), (-0.5, -0.5, 0.5)]),
]


def build_buffers():
    positions, normals, indices = [], [], []
    for normal, corners in FACES:
        base = len(positions)
        for corner in corners:
            positions.append(corner)
            normals.append(normal)
        indices.extend([base, base + 1, base + 2, base, base + 2, base + 3])
    position_bytes = b"".join(struct.pack("<3f", *p) for p in positions)
    normal_bytes = b"".join(struct.pack("<3f", *n) for n in normals)
    index_bytes = b"".join(struct.pack("<H", i) for i in indices)
    return position_bytes, normal_bytes, index_bytes, len(positions), len(indices)


def padded(data: bytes, alignment: int = 4) -> bytes:
    remainder = len(data) % alignment
    return data if remainder == 0 else data + b"\x00" * (alignment - remainder)


def build_gltf() -> dict:
    position_bytes, normal_bytes, index_bytes, vertex_count, index_count = build_buffers()
    blob = b""
    buffer_views = []

    def add_view(data: bytes, target: int):
        nonlocal blob
        offset = len(blob)
        blob += padded(data)
        buffer_views.append(
            {"buffer": 0, "byteOffset": offset, "byteLength": len(data), "target": target}
        )
        return len(buffer_views) - 1

    position_view = add_view(position_bytes, 34962)
    normal_view = add_view(normal_bytes, 34962)
    index_view = add_view(index_bytes, 34963)

    nodes = [
        {"name": "1号楼", "mesh": 0, "translation": [-40.0, 10.0, -40.0], "scale": [60.0, 20.0, 40.0]},
        {"name": "2号楼", "mesh": 0, "translation": [60.0, 7.0, 20.0], "scale": [50.0, 14.0, 30.0]},
        {"name": "东门门架", "mesh": 0, "translation": [-90.0, 3.0, 60.0], "scale": [12.0, 6.0, 4.0]},
    ]
    document = {
        "asset": {"version": "2.0", "generator": "digital-twin-platform/tools/make_site_model.py"},
        "scene": 0,
        "scenes": [{"name": "示范园区", "nodes": list(range(len(nodes)))}],
        "nodes": nodes,
        "meshes": [
            {
                "name": "unit-box",
                "primitives": [
                    {
                        "attributes": {"POSITION": 0, "NORMAL": 1},
                        "indices": 2,
                        "material": 0,
                    }
                ],
            }
        ],
        "materials": [
            {
                "name": "building",
                "pbrMetallicRoughness": {
                    "baseColorFactor": [0.22, 0.34, 0.5, 1.0],
                    "metallicFactor": 0.15,
                    "roughnessFactor": 0.75,
                },
                "doubleSided": False,
            }
        ],
        "accessors": [
            {
                "bufferView": position_view,
                "componentType": 5126,
                "count": vertex_count,
                "type": "VEC3",
                "min": [-0.5, -0.5, -0.5],
                "max": [0.5, 0.5, 0.5],
            },
            {"bufferView": normal_view, "componentType": 5126, "count": vertex_count, "type": "VEC3"},
            {"bufferView": index_view, "componentType": 5123, "count": index_count, "type": "SCALAR"},
        ],
        "bufferViews": buffer_views,
        "buffers": [
            {
                "byteLength": len(blob),
                "uri": "data:application/octet-stream;base64,"
                + base64.b64encode(blob).decode("ascii"),
            }
        ],
    }
    return document


def main() -> int:
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("apps/web/public/models/site.gltf")
    document = build_gltf()
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(document, ensure_ascii=False, indent=2))
    print(f"wrote {output} ({output.stat().st_size} bytes, {len(document['nodes'])} nodes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
