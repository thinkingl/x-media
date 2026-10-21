#!/usr/bin/env python3
"""4K25 CUDA Fireworks test source -> Annex-B H.264 (stdout or file).

Renderer: ./fireworks_cuda.py (particle simulation + splatting + bloom + NV12
in CUDA). Overlay: 5x7 clock/frame counter drawn on NV12 by torch.
"""
import argparse
import os
import signal
import sys
import time

import torch
import PyNvVideoCodec as nvc

from fireworks_cuda import FireworksCuda
from gpu_live import Overlay

W, H, FPS = 3840, 2160, 25


class Nv12Frame:
    def __init__(self, planes):
        self.planes = planes

    def cuda(self):
        return [_Iface(p) for p in self.planes]


class _Iface:
    def __init__(self, t):
        self.t = t

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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--width", type=int, default=W)
    ap.add_argument("--height", type=int, default=H)
    ap.add_argument("--fps", type=int, default=FPS)
    ap.add_argument("--mbps", type=float, default=20.0)
    ap.add_argument("--duration", type=float, default=0.0, help="0 = forever")
    ap.add_argument("--out", default="-", help="'-' = stdout, else .h264 file")
    ap.add_argument("--bursts", type=int, default=32)
    ap.add_argument("--particles", type=int, default=256)
    ap.add_argument("--stats", type=float, default=5.0)
    args = ap.parse_args()

    w, h, fps = args.width, args.height, args.fps
    if (w, h) != (W, H):
        raise SystemExit(f"CUDA renderer is compiled for {W}x{H}")
    bitrate = max(128_000, int(args.mbps * 1_000_000))

    renderer = FireworksCuda(w, h, args.bursts, args.particles)
    overlay = Overlay(h, 8)
    enc = nvc.CreateEncoder(
        w, h, "NV12", False,
        codec="h264", preset="P4", fps=fps,
        rc="cbr", bitrate=bitrate, maxbitrate=bitrate, vbvbufsize=bitrate,
        gop=fps, bf=0,
    )

    nv12 = renderer.nv12
    y = nv12[:h].view(h, w, 1)
    uv = nv12[h:].view(h // 2, w // 2, 2)

    pad = 6
    ox, oy = 40, 40
    bw = 12 * overlay.glyph_w + 2 * pad
    bh = 2 * overlay.glyph_h + 3 * pad

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
            renderer.step(i / fps)
            # timestamp/frame counter box on Y/UV
            y[oy:oy + bh, ox:ox + bw, 0] = 16
            uv[oy // 2:(oy + bh) // 2, ox // 2:(ox + bw) // 2, :] = 128
            now = time.time()
            ms = int((now - int(now)) * 1000)
            clock = time.strftime("%H:%M:%S", time.localtime(now)) + f".{ms:03d}"
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
                next_t = now

            now = time.perf_counter()
            if args.stats > 0 and now - stat_t >= args.stats:
                ts = os.times()
                cpu = (ts.user + ts.system) - (cpu_last.user + cpu_last.system)
                wall = now - stat_t
                print(
                    f"[4k-fw] frame={i} enc={stat_frames} fps={stat_frames / wall:.1f} "
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
        renderer.close()
        elapsed = time.perf_counter() - t0
        print(f"[4k-fw] done frames={total} wall={elapsed:.1f}s avg_fps={total / max(elapsed, 1e-9):.2f}",
              file=sys.stderr, flush=True)


if __name__ == "__main__":
    main()
