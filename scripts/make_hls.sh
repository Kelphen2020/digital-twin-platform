#!/usr/bin/env bash
# Generate the local HLS test stream used by the P1.0 demo (no real camera).
#
# Output: apps/web/public/hls/test.m3u8 + segments, served by the Vite dev
# server (or any static server) at /hls/test.m3u8.
#
# Real deployments replace camera.stream_url with the media server output
# (ZLMediaKit WebRTC/FLV/HLS) — the web client only consumes the URL.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="apps/web/public/hls"
DURATION="${1:-60}"
BITRATE="${HLS_BITRATE:-1200k}"

if ! command -v ffmpeg >/dev/null 2>&1; then
	echo "ffmpeg is required to generate the HLS test stream" >&2
	exit 2
fi

mkdir -p "$OUT"
rm -f "$OUT"/*.ts "$OUT"/test.m3u8

ffmpeg -y -loglevel error \
	-f lavfi -i "testsrc2=size=1280x720:rate=25" \
	-f lavfi -i "sine=frequency=440:sample_rate=48000" \
	-t "$DURATION" \
	-c:v libx264 -preset veryfast -pix_fmt yuv420p -g 50 -sc_threshold 0 \
	-b:v "$BITRATE" -maxrate "$BITRATE" -bufsize 2400k \
	-c:a aac -b:a 96k \
	-f hls -hls_time 2 -hls_playlist_type vod -hls_flags independent_segments \
	-hls_segment_filename "$OUT/seg_%03d.ts" \
	"$OUT/test.m3u8"

echo "HLS test stream written to $OUT/test.m3u8 (${DURATION}s)"
ls -lh "$OUT" | head -3
