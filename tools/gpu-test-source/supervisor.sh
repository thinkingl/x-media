#!/usr/bin/env bash
set -u

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${LOG_DIR:-/tmp/x-media-gpu-test}"
RTSP_URL="${RTSP_URL:-rtsp://127.0.0.1:5544/gpu_test}"
FFMPEG="${FFMPEG:-/usr/local/bin/ffmpeg}"
MBPS="${MBPS:-20}"

mkdir -p "$LOG_DIR"
export PATH="$HOME/.local/bin:$PATH"

while true; do
  echo "[supervisor] start $(date '+%F %T')" >> "$LOG_DIR/supervisor.log"
  python3 "$DIR/gpu_live_4k_fireworks.py" --mbps "$MBPS" --duration 0 \
      2>>"$LOG_DIR/gen.log" | \
    "$FFMPEG" -hide_banner -v warning -fflags +genpts -r 25 \
      -f h264 -i pipe:0 -c:v copy -muxdelay 0 -muxpreload 0 \
      -f rtsp -rtsp_transport tcp "$RTSP_URL" \
      >>"$LOG_DIR/pub.log" 2>&1
  rc=$?
  echo "[supervisor] pipeline exited rc=$rc, restart in 2s $(date '+%F %T')" \
      >> "$LOG_DIR/supervisor.log"
  sleep 2
done
