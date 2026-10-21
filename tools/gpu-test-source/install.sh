#!/usr/bin/env bash
set -euo pipefail

# Runtime Python dependencies. PyTorch itself is expected to be a CUDA build
# already installed on the machine (the project uses torch 2.9.1+cu126 here).
python3 -m pip install --user --no-deps PyNvVideoCodec==2.2.3
python3 -m pip install --user ninja

python3 - <<'PY'
import torch
try:
    import PyNvVideoCodec as nvc
except ImportError as exc:
    raise SystemExit(f"PyNvVideoCodec import failed: {exc}")
print("torch:", torch.__version__, "cuda:", torch.cuda.is_available())
print("PyNvVideoCodec:", nvc.__version__, nvc.__file__)
if not torch.cuda.is_available():
    raise SystemExit("CUDA is not available to PyTorch")
PY

cat <<'EOF'

Installed. Ensure ffmpeg is available (default supervisor uses
/usr/local/bin/ffmpeg; override with FFMPEG=/path/to/ffmpeg).

First run compiles the CUDA extension (1-2 min):
  python3 gpu_live_4k_fireworks.py --duration 2 --out /tmp/fw4k.h264
EOF
