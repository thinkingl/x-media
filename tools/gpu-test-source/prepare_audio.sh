#!/usr/bin/env bash
set -euo pipefail

# Convert the source music to AAC-LC ADTS for the test source.
# The resulting file must NOT be committed (copyright/large binary).
#
# Usage:
#   ./prepare_audio.sh [source.flac] [output.aac]

SRC="${1:-/tmp/opencode/黄霄雲 - 烟火里的尘埃.flac}"
OUT="${2:-/tmp/opencode/fireworks_audio.aac}"
FFMPEG="${FFMPEG:-/usr/local/bin/ffmpeg}"

if [ ! -f "$SRC" ]; then
  echo "source not found: $SRC" >&2
  exit 1
fi

"$FFMPEG" -hide_banner -v error -y \
  -i "$SRC" -vn \
  -c:a aac -profile:a aac_low -ar 48000 -ac 2 -b:a 128k \
  -f adts "$OUT"

ls -lh "$OUT"
ffprobe -v error -show_entries stream=codec_name,profile,sample_rate,channels,bit_rate \
  -show_entries format=duration,size -of default=nw=1 "$OUT"
