#!/usr/bin/env bash
set -u

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${LOG_DIR:-/tmp/x-media-gpu-test}"
RTSP_URL="${RTSP_URL:-rtsp://127.0.0.1:5544/gpu_test}"
FFMPEG="${FFMPEG:-/usr/local/bin/ffmpeg}"
MBPS="${MBPS:-20}"
AUDIO_FILE="${AUDIO_FILE:-/tmp/opencode/fireworks_audio.aac}"

mkdir -p "$LOG_DIR"
export PATH="$HOME/.local/bin:$PATH"

if [ -f "$AUDIO_FILE" ]; then
  AUDIO_INPUT=(-stream_loop -1 -re -i "$AUDIO_FILE")
  AUDIO_MAP=(-map 0:v:0 -map 1:a:0 -c:a aac -b:a 128k -ar 48000 -ac 2)
  echo "[supervisor] audio: $AUDIO_FILE (loop)" >> "$LOG_DIR/supervisor.log"
else
  AUDIO_INPUT=()
  AUDIO_MAP=(-map 0:v:0)
  echo "[supervisor] audio: disabled (AUDIO_FILE not found: $AUDIO_FILE)" >> "$LOG_DIR/supervisor.log"
fi

while true; do
  echo "[supervisor] start $(date '+%F %T')" >> "$LOG_DIR/supervisor.log"
  python3 "$DIR/gpu_live_4k_fireworks.py" --mbps "$MBPS" --duration 0 \
      2>>"$LOG_DIR/gen.log" | \
    "$FFMPEG" -hide_banner -v warning -fflags +genpts -r 25 \
      -f h264 -c:v h264_cuvid -i pipe:0 \
      "${AUDIO_INPUT[@]}" \
      "${AUDIO_MAP[@]}" \
      -c:v h264_nvenc -preset p4 -rc cbr \
      -b:v "${MBPS}M" -maxrate "${MBPS}M" -bufsize "${MBPS}M" \
      -g 25 -bf 0 -muxdelay 0 -muxpreload 0 -avoid_negative_ts make_zero \
      -f rtsp -rtsp_transport tcp "$RTSP_URL" \
      >>"$LOG_DIR/pub.log" 2>&1
  rc=$?
  echo "[supervisor] pipeline exited rc=$rc, restart in 2s $(date '+%F %T')" \
      >> "$LOG_DIR/supervisor.log"
  sleep 2
done
