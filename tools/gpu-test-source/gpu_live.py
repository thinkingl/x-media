#!/usr/bin/env python3
"""Continuous GPU test-stream generator.

Generates NV12 frames on the GPU (random pattern + millisecond clock),
encodes them zero-copy with NVENC (PyNvVideoCodec), and writes an
Annex-B H.264 elementary stream to stdout (or a file).

Typical use (publish to the x-media RTSP server, then pull as RTSP input):

  python3 gpu_live.py --mbps 8 --pattern mixed \
    | ffmpeg -hide_banner -v warning -fflags +genpts -r 25 \
        -f h264 -i pipe:0 -c:v copy -muxdelay 0 -muxpreload 0 \
        -f rtsp -rtsp_transport tcp rtsp://127.0.0.1:5544/gpu_test

Requirements: PyTorch (CUDA), PyNvVideoCodec (NVENC 12.1+ driver).
"""
import argparse
import os
import random
import signal
import sys
import time

import torch
import PyNvVideoCodec as nvc


class CudaFrame:
    """Expose a torch CUDA tensor as __cuda_array_interface__."""

    def __init__(self, tensor):
        self.t = tensor

    @property
    def __cuda_array_interface__(self):
        t = self.t
        return {
            "shape": tuple(t.shape),
            "strides": tuple(s * t.element_size() for s in t.stride()),
            "typestr": "|u1",
            "data": (t.data_ptr(), False),
            "version": 3,
        }


class Nv12Frame:
    """PyNvVideoCodec GPU input: Y and interleaved UV must share one allocation."""

    def __init__(self, planes):
        self.planes = planes

    def cuda(self):
        return [CudaFrame(p) for p in self.planes]


FONT = {
    "0": ["01110", "10001", "10011", "10101", "11001", "10001", "01110"],
    "1": ["00100", "01100", "00100", "00100", "00100", "00100", "01110"],
    "2": ["01110", "10001", "00001", "00010", "00100", "01000", "11111"],
    "3": ["11110", "00001", "00001", "01110", "00001", "00001", "11110"],
    "4": ["00010", "00110", "01010", "10010", "11111", "00010", "00010"],
    "5": ["11111", "10000", "11110", "00001", "00001", "10001", "01110"],
    "6": ["00110", "01000", "10000", "11110", "10001", "10001", "01110"],
    "7": ["11111", "00001", "00010", "00100", "01000", "01000", "01000"],
    "8": ["01110", "10001", "10001", "01110", "10001", "10001", "01110"],
    "9": ["01110", "10001", "10001", "01111", "00001", "00010", "01100"],
    ":": ["00000", "00100", "00100", "00000", "00100", "00100", "00000"],
    ".": ["00000", "00000", "00000", "00000", "00000", "00110", "00110"],
    "F": ["11111", "10000", "10000", "11110", "10000", "10000", "10000"],
}
CHARS = "0123456789:.F"
CHAR_IDX = {c: i for i, c in enumerate(CHARS)}


class Overlay:
    def __init__(self, height, scale):
        self.scale = scale
        self.glyph_h = 7 * scale
        self.glyph_w = 5 * scale
        self.font = torch.tensor(
            [[[int(v) for v in row] for row in FONT[c]] for c in CHARS],
            dtype=torch.uint8,
            device="cuda",
        )
        self.fg = torch.tensor(235, dtype=torch.uint8, device="cuda")
        self.bg = torch.tensor(16, dtype=torch.uint8, device="cuda")
        self.height = height

    def _row(self, text):
        idx = torch.tensor([CHAR_IDX[c] for c in text], dtype=torch.long, device="cuda")
        g = self.font[idx]  # (n, 7, 5)
        row = g.permute(1, 0, 2).reshape(7, len(text) * 5)
        return row.repeat_interleave(self.scale, 0).repeat_interleave(self.scale, 1)

    def draw(self, y, uv, x0, y0, text):
        row = self._row(text)
        h, w = row.shape
        y[y0:y0 + h, x0:x0 + w, 0] = torch.where(row > 0, self.fg, self.bg)
        uv[y0 // 2:(y0 + h) // 2, x0 // 2:(x0 + w) // 2, :] = 128


def gen_frame(y, uv, w, h, idx, rnd, pattern, noise_amp):
    if pattern == "noise":
        y.random_(16, 236)
        uv.random_(16, 241)
        return

    y.fill_(24)
    uv.fill_(128)
    # Random rectangular patches.
    for _ in range(6):
        rw = rnd.randrange(80, max(81, w // 3))
        rh = rnd.randrange(60, max(61, h // 4))
        x = rnd.randrange(0, w - rw)
        yy = rnd.randrange(0, h - rh)
        v = rnd.randrange(50, 236)
        y[yy:yy + rh, x:x + rw, 0] = v
        u = rnd.randrange(80, 176)
        vv = rnd.randrange(80, 176)
        uv[yy // 2:(yy + rh) // 2, x // 2:(x + rw) // 2, 0] = u
        uv[yy // 2:(yy + rh) // 2, x // 2:(x + rw) // 2, 1] = vv

    if pattern == "mixed" and noise_amp > 0:
        noise = torch.randint(0, noise_amp, (h, w, 1), dtype=torch.uint8, device="cuda")
        y.add_(noise)
        torch.clamp_(y, 16, 235)

    # Moving bars: makes freezes / frame reordering obvious.
    bx = (idx * 12) % w
    y[:, bx:bx + 80, 0] = 235
    by = (idx * 9) % h
    y[by:by + 60, :, 0] = 200


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--width", type=int, default=1920)
    ap.add_argument("--height", type=int, default=1080)
    ap.add_argument("--fps", type=int, default=25)
    ap.add_argument("--gop", type=int, default=25)
    ap.add_argument("--mbps", type=float, default=8.0, help="target bitrate in Mbit/s")
    ap.add_argument("--rc", choices=["cbr", "vbr"], default="cbr")
    ap.add_argument("--pattern", choices=["noise", "mixed", "blocks"], default="mixed")
    ap.add_argument("--noise-amp", type=int, default=12)
    ap.add_argument("--duration", type=float, default=0, help="0 = run forever")
    ap.add_argument("--out", default="-", help="'-' = stdout, otherwise a .h264 file")
    ap.add_argument("--no-overlay", action="store_true")
    ap.add_argument("--scale", type=int, default=0, help="overlay font scale (0 = auto)")
    ap.add_argument("--stats", type=float, default=5.0, help="stats interval seconds")
    args = ap.parse_args()

    if not torch.cuda.is_available():
        raise SystemExit("CUDA is not available")
    if args.width % 2 or args.height % 2:
        raise SystemExit("width/height must be even for NV12")

    w, h, fps = args.width, args.height, args.fps
    bitrate_bps = max(64_000, int(args.mbps * 1_000_000))

    enc = nvc.CreateEncoder(
        w, h, "NV12", False,
        codec="h264", preset="P4", fps=fps,
        rc=args.rc,
        bitrate=bitrate_bps,
        maxbitrate=bitrate_bps if args.rc == "cbr" else bitrate_bps * 2,
        vbvbufsize=bitrate_bps,
        gop=args.gop, bf=0,
    )

    # Single NV12 allocation: Y plane followed by interleaved UV plane.
    buf = torch.empty((h * 3 // 2, w), dtype=torch.uint8, device="cuda")
    y = buf[:h].view(h, w, 1)
    uv = buf[h:].view(h // 2, w // 2, 2)

    scale = args.scale or max(2, h // 240)
    overlay = None
    if not args.no_overlay:
        overlay = Overlay(h, scale)
        pad = 6
        box_w = 12 * overlay.glyph_w + 2 * pad
        box_h = 2 * overlay.glyph_h + 3 * pad
        ox, oy = 20, 20
        if box_w + ox > w or box_h + oy > h:
            raise SystemExit("overlay does not fit; lower --scale")

    rnd = random.Random(int(time.time() * 1000))
    stop = False

    def on_signal(_sig, _frm):
        nonlocal stop
        stop = True

    signal.signal(signal.SIGINT, on_signal)
    signal.signal(signal.SIGTERM, on_signal)

    if args.out == "-":
        out = sys.stdout.buffer
        flush_each = True
    else:
        out = open(args.out, "wb")
        flush_each = False

    n = int(args.duration * fps) if args.duration > 0 else None
    t0 = time.perf_counter()
    next_t = t0
    stat_t = t0
    stat_frames = 0
    stat_bytes = 0
    cpu_last = os.times()
    total = 0

    try:
        i = 0
        while not stop and (n is None or i < n):
            gen_frame(y, uv, w, h, i, rnd, args.pattern, args.noise_amp)
            if overlay is not None:
                now = time.time()
                ms = int((now - int(now)) * 1000)
                clock = time.strftime("%H:%M:%S", time.localtime(now)) + f".{ms:03d}"
                pad = 6
                ox, oy = 20, 20
                bw = 12 * overlay.glyph_w + 2 * pad
                bh = 2 * overlay.glyph_h + 3 * pad
                y[oy:oy + bh, ox:ox + bw, 0] = 16
                uv[oy // 2:(oy + bh) // 2, ox // 2:(ox + bw) // 2, :] = 128
                overlay.draw(y, uv, ox + pad, oy + pad, clock)
                overlay.draw(y, uv, ox + pad, oy + 2 * pad + overlay.glyph_h, f"F{i:06d}")

            for pkt in enc.Encode(Nv12Frame([y, uv])):
                data = pkt["data"]
                out.write(data)
                stat_bytes += len(data)
                stat_frames += 1
            total += 1
            if flush_each:
                out.flush()

            next_t += 1.0 / fps
            now = time.perf_counter()
            if now < next_t:
                time.sleep(next_t - now)
            elif now - next_t > 0.5:
                next_t = now  # we fell far behind; drop schedule debt

            now = time.perf_counter()
            if args.stats > 0 and now - stat_t >= args.stats:
                ts = os.times()
                cpu = (ts.user + ts.system) - (cpu_last.user + cpu_last.system)
                wall = now - stat_t
                print(
                    f"[gpu-live] frame={i} enc={stat_frames} fps={stat_frames / wall:.1f} "
                    f"bitrate={stat_bytes * 8 / wall / 1e6:.1f}Mbps cpu={cpu / wall * 100:.1f}%",
                    file=sys.stderr, flush=True,
                )
                stat_t = now
                stat_frames = 0
                stat_bytes = 0
                cpu_last = ts
            i += 1
    except BrokenPipeError:
        pass
    finally:
        try:
            for pkt in enc.EndEncode():
                out.write(pkt["data"])
        except Exception:
            pass
        try:
            if flush_each:
                out.flush()
        except Exception:
            pass
        if args.out != "-":
            out.close()
        elapsed = time.perf_counter() - t0
        print(
            f"[gpu-live] done frames={total} wall={elapsed:.1f}s avg_fps={total / max(elapsed, 1e-9):.2f}",
            file=sys.stderr, flush=True,
        )


if __name__ == "__main__":
    main()
