import os, math, time
os.environ["PATH"] = os.path.expanduser("~/.local/bin") + ":" + os.environ.get("PATH", "")
os.environ.setdefault("MAX_JOBS", "2")
import torch
from torch.utils.cpp_extension import load_inline
if torch.cuda.is_available():
    _cap = torch.cuda.get_device_capability()
    os.environ.setdefault("TORCH_CUDA_ARCH_LIST", f"{_cap[0]}.{_cap[1]}")

cuda_src = r'''
#include <torch/extension.h>
#include <cuda_runtime.h>
#include <cstdint>

__device__ __forceinline__ void palette(float t, float &r, float &g, float &b) {
    const float p[6] = {0.0f, 0.30f, 0.52f, 0.72f, 0.88f, 1.0f};
    const float cr[6] = {0.005f, 0.010f, 0.000f, 0.300f, 0.850f, 1.000f};
    const float cg[6] = {0.008f, 0.080f, 0.380f, 0.080f, 0.250f, 0.880f};
    const float cb[6] = {0.025f, 0.180f, 0.480f, 0.480f, 0.420f, 0.620f};
    int i = 0;
    while (i < 5 && t > p[i+1]) i++;
    float f = (t - p[i]) / (p[i+1] - p[i]);
    f = f < 0.f ? 0.f : (f > 1.f ? 1.f : f);
    r = cr[i] + f * (cr[i+1] - cr[i]);
    g = cg[i] + f * (cg[i+1] - cg[i]);
    b = cb[i] + f * (cb[i+1] - cb[i]);
}

__global__ void kaleido_kernel(uint8_t *out, int W, int H, float t) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;
    float nx = ((x + 0.5f) / W * 2.f - 1.f) * 1.7777778f;
    float ny = ((y + 0.5f) / H * 2.f - 1.f);
    float r = sqrtf(nx*nx + ny*ny);
    float a = atan2f(ny, nx);
    float seg = 2.f * 3.14159265f / 9.f;
    float a2 = fabsf(fmodf(a + t * 0.14f, seg) - seg * 0.5f);
    float u = r * cosf(a2 * 3.f + t * 0.45f);
    float v = r * sinf(a2 * 3.f - t * 0.35f);
    float val = sinf(9.f*r - t*1.8f) + 0.65f*sinf(14.f*u + t*0.8f) + 0.55f*cosf(11.f*v - t*1.2f);
    float n = 0.5f + 0.5f * sinf(val * 1.25f + 0.6f);
    float R,G,B; palette(n, R,G,B);
    float vig = 1.f - 0.5f * fminf(fmaxf(r / 1.35f, 0.f), 1.f);
    R *= vig; G *= vig; B *= vig;
    int o = (y * W + x) * 3;
    out[o] = (uint8_t)(fminf(fmaxf(R,0.f),1.f)*255.f);
    out[o+1] = (uint8_t)(fminf(fmaxf(G,0.f),1.f)*255.f);
    out[o+2] = (uint8_t)(fminf(fmaxf(B,0.f),1.f)*255.f);
}

__global__ void aurora_kernel(uint8_t *out, int W, int H, float t) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;
    float nx = ((x + 0.5f) / W * 2.f - 1.f) * 1.7777778f;
    float ny = ((y + 0.5f) / H * 2.f - 1.f);
    float base = sinf(3.2f*nx + 1.1f*t) + 0.8f*sinf(4.1f*ny - 0.7f*t)
               + 0.5f*sinf(2.4f*(nx+ny) + 1.6f*t) + 0.4f*sinf(5.3f*nx*ny - 0.3f*t);
    float n = 1.f / (1.f + expf(-1.1f * base));
    float R,G,B; palette(n, R,G,B);
    uint32_t h = (uint32_t)x * 374761393u + (uint32_t)y * 668265263u;
    h = (h ^ (h >> 13)) * 1274126177u;
    if ((h & 0xFFFFu) < 7u) { R += 0.85f; G += 0.85f; B += 0.85f; }
    int o = (y * W + x) * 3;
    out[o] = (uint8_t)(fminf(fmaxf(R,0.f),1.f)*255.f);
    out[o+1] = (uint8_t)(fminf(fmaxf(G,0.f),1.f)*255.f);
    out[o+2] = (uint8_t)(fminf(fmaxf(B,0.f),1.f)*255.f);
}

__global__ void julia_kernel(uint8_t *out, int W, int H, float t) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;
    float nx = ((x + 0.5f) / W * 2.f - 1.f) * 1.6f - 0.55f;
    float ny = ((y + 0.5f) / H * 2.f - 1.f) * 1.1f;
    float cr = -0.8f + 0.02f * cosf(t * 0.3f);
    float ci = 0.156f + 0.02f * sinf(t * 0.3f);
    float zx = nx, zy = ny;
    int it = 0;
    for (; it < 64; ++it) {
        float zx2 = zx*zx - zy*zy + cr;
        zy = 2.f*zx*zy + ci;
        zx = zx2;
        if (zx*zx + zy*zy > 4.f) break;
    }
    float n = (float)it / 64.f;
    float R,G,B;
    if (it >= 64) { R = 0.005f; G = 0.005f; B = 0.015f; }
    else { palette(n, R,G,B); }
    int o = (y * W + x) * 3;
    out[o] = (uint8_t)(fminf(fmaxf(R,0.f),1.f)*255.f);
    out[o+1] = (uint8_t)(fminf(fmaxf(G,0.f),1.f)*255.f);
    out[o+2] = (uint8_t)(fminf(fmaxf(B,0.f),1.f)*255.f);
}

torch::Tensor kaleido(torch::Tensor out, double t) {
    int H = out.size(0), W = out.size(1);
    dim3 block(16,16); dim3 grid((W+15)/16,(H+15)/16);
    kaleido_kernel<<<grid, block>>>(out.data_ptr<uint8_t>(), W, H, (float)t);
    return out;
}
torch::Tensor aurora(torch::Tensor out, double t) {
    int H = out.size(0), W = out.size(1);
    dim3 block(16,16); dim3 grid((W+15)/16,(H+15)/16);
    aurora_kernel<<<grid, block>>>(out.data_ptr<uint8_t>(), W, H, (float)t);
    return out;
}
torch::Tensor julia(torch::Tensor out, double t) {
    int H = out.size(0), W = out.size(1);
    dim3 block(16,16); dim3 grid((W+15)/16,(H+15)/16);
    julia_kernel<<<grid, block>>>(out.data_ptr<uint8_t>(), W, H, (float)t);
    return out;
}
'''

cpp_src = r'''
torch::Tensor kaleido(torch::Tensor out, double t);
torch::Tensor aurora(torch::Tensor out, double t);
torch::Tensor julia(torch::Tensor out, double t);
'''

if __name__ == "__main__":
    print("compiling CUDA kernels (first run may take 1-2 min)...")
    mod = load_inline(
        name="gpu_pattern_kernels",
        cpp_sources=cpp_src,
        cuda_sources=cuda_src,
        functions=["kaleido", "aurora", "julia"],
        extra_cuda_cflags=["-O3", "--use_fast_math"],
        verbose=False,
    )
    print("compiled")
    OUT_DIR = os.environ.get("PATTERN_OUT", "/tmp/x-media-gpu-patterns")
    os.makedirs(OUT_DIR, exist_ok=True)

    H, W = 2160, 3840
    out = torch.empty((H, W, 3), dtype=torch.uint8, device="cuda")

    def bench(name, fn, iters=6):
        fn(); torch.cuda.synchronize()
        t0 = time.perf_counter(); c0 = time.process_time()
        for _ in range(iters):
            fn()
        torch.cuda.synchronize()
        wall = (time.perf_counter()-t0)/iters*1000
        cpu = (time.process_time()-c0)/iters*1000
        print(f"{name}: wall={wall:.1f}ms cpu={cpu:.1f}ms -> 25fps CPU={wall/40*100:.0f}%")
        return wall

    for name, fn in [("aurora", lambda: mod.aurora(out, 0.3)),
                     ("kaleido", lambda: mod.kaleido(out, 0.3)),
                     ("julia", lambda: mod.julia(out, 0.3))]:
        bench(name, fn)
        arr = out.cpu().numpy()
        from PIL import Image
        Image.fromarray(arr).save(os.path.join(OUT_DIR, f"cuda_{name}.png"))
    print("saved samples")
