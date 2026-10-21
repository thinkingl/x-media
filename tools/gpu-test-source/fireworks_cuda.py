"""4K CUDA fireworks renderer / NVENC test source.

Particle simulation + splatting + bloom + NV12 conversion in CUDA.
The C++ wrapper owns device buffers; Python calls fw_init/fw_step.
"""
import os, sys, time
os.environ["PATH"] = os.path.expanduser("~/.local/bin") + ":" + os.environ.get("PATH", "")
os.environ.setdefault("MAX_JOBS", "2")
import torch
if torch.cuda.is_available():
    _cap = torch.cuda.get_device_capability()
    os.environ.setdefault("TORCH_CUDA_ARCH_LIST", f"{_cap[0]}.{_cap[1]}")
from torch.utils.cpp_extension import load_inline

cuda_src = r'''
#include <torch/extension.h>
#include <cuda_runtime.h>
#include <cmath>
#include <cstdint>
#include <memory>

#define PI_F 6.28318530718f

struct BurstState {
    float ox, oy;
    float cr, cg, cb;
    float birth, life, z, decay, wind;
    int count;
};

struct ParticleState {
    float dx, dy;
    float speed, size, bright, phase, freq, life_scale;
};

__constant__ float PALETTE[7][3] = {
    {1.00f, 0.82f, 0.30f},
    {0.30f, 0.85f, 1.00f},
    {1.00f, 0.32f, 0.60f},
    {0.55f, 1.00f, 0.45f},
    {1.00f, 0.55f, 0.18f},
    {0.75f, 0.55f, 1.00f},
    {1.00f, 0.95f, 0.80f},
};

__device__ __forceinline__ uint32_t hash32(uint32_t x) {
    x ^= x >> 16;
    x *= 0x7feb352dU;
    x ^= x >> 15;
    x *= 0x846ca68bU;
    x ^= x >> 16;
    return x;
}

__device__ __forceinline__ float rnd(uint32_t &s) {
    s = hash32(s + 0x9e3779b9U);
    return (s & 0x00FFFFFFU) * (1.0f / 16777216.0f);
}

__device__ __forceinline__ float3 palette_mix(int i1, int i2, float mix) {
    return make_float3(
        PALETTE[i1][0] * mix + PALETTE[i2][0] * (1.0f - mix),
        PALETTE[i1][1] * mix + PALETTE[i2][1] * (1.0f - mix),
        PALETTE[i1][2] * mix + PALETTE[i2][2] * (1.0f - mix));
}

__global__ void update_bursts_kernel(
        BurstState *bursts, ParticleState *parts,
        int B, int P, float W, float H, float t,
        uint32_t frame, bool initial) {
    int b = blockIdx.x;
    if (b >= B) return;
    BurstState &bs = bursts[b];
    bool dead = initial || (t >= bs.birth + bs.life);
    if (!dead) return;

    __shared__ BurstState sh;
    if (threadIdx.x == 0) {
        uint32_t seed = hash32((uint32_t)(t * 1000.0f) ^ (b * 0x9e3779b9U) ^ (frame * 0x85ebca6bU));
        float z = 0.25f + 0.75f * powf(rnd(seed), 0.8f);
        int i1 = (int)(rnd(seed) * 7.0f); if (i1 > 6) i1 = 6;
        int i2 = (int)(rnd(seed) * 7.0f); if (i2 > 6) i2 = 6;
        float3 col = palette_mix(i1, i2, rnd(seed));
        sh.ox = (0.06f + 0.88f * rnd(seed)) * W;
        sh.oy = (0.10f + 0.58f * rnd(seed)) * H + (1.0f - z) * 0.10f * H;
        sh.z = z;
        sh.cr = col.x; sh.cg = col.y; sh.cb = col.z;
        sh.birth = initial ? (t + rnd(seed) * 4.0f - 4.0f) : (t + rnd(seed) * 1.0f);
        sh.life = (2.4f + 3.2f * z) * (0.9f + 0.4f * rnd(seed));
        sh.decay = 0.18f + 0.35f * rnd(seed);
        sh.wind = (rnd(seed) - 0.5f) * 20.0f;
        sh.count = (int)(70.0f + 180.0f * z);
        if (sh.count > P) sh.count = P;
        bursts[b] = sh;
    }
    __syncthreads();
    BurstState s = sh;
    int p = threadIdx.x;
    if (p >= P) return;
    uint32_t seed = hash32((uint32_t)(b * P + p) ^ (uint32_t)(t * 1000.0f) ^ (frame * 0x27d4eb2dU));
    float ang = rnd(seed) * 2.0f * PI_F;
    ParticleState ps;
    ps.dx = cosf(ang); ps.dy = sinf(ang);
    ps.speed = (70.0f + 560.0f * s.z) * (0.35f + 0.95f * rnd(seed));
    ps.size = 0.6f + 0.9f * s.z;
    ps.bright = (0.8f + 2.0f * s.z) * (0.55f + 1.3f * rnd(seed));
    ps.phase = rnd(seed) * PI_F;
    ps.freq = 4.0f + 10.0f * rnd(seed);
    ps.life_scale = 0.85f + 0.30f * rnd(seed);
    parts[b * P + p] = ps;
}

__global__ void decay_buffers_kernel(float *core, int ncore, float dc, float *glow, int nglow, float dg) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < ncore) core[i] *= dc;
    if (i < nglow) glow[i] *= dg;
}

__global__ void render_particles_kernel(
        const BurstState *bursts, const ParticleState *parts,
        float *core, float *glow,
        int W, int H, int GW, int GH, int B, int P, float t) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= B * P) return;
    int b = idx / P, p = idx - b * P;
    BurstState s = bursts[b];
    if (p >= s.count) return;
    ParticleState ps = parts[idx];
    float age = t - s.birth;
    if (age < 0.0f) return;
    float plife = s.life * ps.life_scale;
    if (age >= plife) return;

    float k = 0.7f + 0.7f * s.z;
    float af = (1.0f - expf(-k * age)) / k;
    float px = s.ox + ps.dx * ps.speed * af + s.wind * age * age;
    float py = s.oy + ps.dy * ps.speed * af + 0.5f * (45.0f + 70.0f * s.z) * age * age;

    float lf = age / plife;
    float fade = expf(-s.decay * age) * (1.0f - lf);
    if (fade < 0.0f) fade = 0.0f;
    float tw = 0.78f + 0.22f * sinf(ps.freq * age + ps.phase);
    float br = ps.bright * fade * tw * (1.2f + 2.5f * s.z);
    float3 col = make_float3(s.cr * br, s.cg * br, s.cb * br);

    int xi = (int)px, yi = (int)py;
    if (xi < 5 || xi >= W - 5 || yi < 5 || yi >= H - 5) return;

    #pragma unroll
    for (int dy = -2; dy <= 2; ++dy) {
        #pragma unroll
        for (int dx = -2; dx <= 2; ++dx) {
            float w = expf(-(dx * dx + dy * dy) * 0.5f) * 0.16f;
            int o = ((yi + dy) * W + (xi + dx)) * 3;
            atomicAdd(&core[o + 0], col.x * w);
            atomicAdd(&core[o + 1], col.y * w);
            atomicAdd(&core[o + 2], col.z * w);
        }
    }

    int gx = xi >> 2, gy = yi >> 2;
    if (gx < 5 || gx >= GW - 5 || gy < 5 || gy >= GH - 5) return;
    #pragma unroll
    for (int dy = -2; dy <= 2; ++dy) {
        #pragma unroll
        for (int dx = -2; dx <= 2; ++dx) {
            float w = expf(-(dx * dx + dy * dy) * 0.4f) * 0.09f;
            int o = ((gy + dy) * GW + (gx + dx)) * 3;
            atomicAdd(&glow[o + 0], col.x * w);
            atomicAdd(&glow[o + 1], col.y * w);
            atomicAdd(&glow[o + 2], col.z * w);
        }
    }
}

__global__ void blur_glow_kernel(const float *src, float *dst, int GW, int GH) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= GW || y >= GH) return;
    const float K[3][3] = {{1.0f, 2.0f, 1.0f}, {2.0f, 4.0f, 2.0f}, {1.0f, 2.0f, 1.0f}};
    float r = 0.0f, g = 0.0f, b = 0.0f;
    #pragma unroll
    for (int dy = -1; dy <= 1; ++dy) {
        int sy = y + dy; sy = sy < 0 ? 0 : (sy >= GH ? GH - 1 : sy);
        #pragma unroll
        for (int dx = -1; dx <= 1; ++dx) {
            int sx = x + dx; sx = sx < 0 ? 0 : (sx >= GW ? GW - 1 : sx);
            float w = K[dy + 1][dx + 1] * (1.0f / 16.0f);
            int o = (sy * GW + sx) * 3;
            r += src[o + 0] * w; g += src[o + 1] * w; b += src[o + 2] * w;
        }
    }
    int o = (y * GW + x) * 3;
    dst[o + 0] = r; dst[o + 1] = g; dst[o + 2] = b;
}

__device__ __forceinline__ float3 sample_bilinear(const float *buf, int GW, int GH, float fx, float fy) {
    fx = fminf(fmaxf(fx - 0.5f, 0.0f), (float)(GW - 1));
    fy = fminf(fmaxf(fy - 0.5f, 0.0f), (float)(GH - 1));
    int x0 = (int)floorf(fx), y0 = (int)floorf(fy);
    int x1 = min(x0 + 1, GW - 1), y1 = min(y0 + 1, GH - 1);
    float tx = fx - x0, ty = fy - y0;
    float3 a = make_float3(buf[(y0 * GW + x0) * 3 + 0], buf[(y0 * GW + x0) * 3 + 1], buf[(y0 * GW + x0) * 3 + 2]);
    float3 b = make_float3(buf[(y0 * GW + x1) * 3 + 0], buf[(y0 * GW + x1) * 3 + 1], buf[(y0 * GW + x1) * 3 + 2]);
    float3 c = make_float3(buf[(y1 * GW + x0) * 3 + 0], buf[(y1 * GW + x0) * 3 + 1], buf[(y1 * GW + x0) * 3 + 2]);
    float3 d = make_float3(buf[(y1 * GW + x1) * 3 + 0], buf[(y1 * GW + x1) * 3 + 1], buf[(y1 * GW + x1) * 3 + 2]);
    float3 ab = make_float3(a.x + (b.x - a.x) * tx, a.y + (b.y - a.y) * tx, a.z + (b.z - a.z) * tx);
    float3 cd = make_float3(c.x + (d.x - c.x) * tx, c.y + (d.y - c.y) * tx, c.z + (d.z - c.z) * tx);
    return make_float3(ab.x + (cd.x - ab.x) * ty, ab.y + (cd.y - ab.y) * ty, ab.z + (cd.z - ab.z) * ty);
}

__global__ void composite_kernel(const float *core, const float *glow, int W, int H, int GW, int GH, uint8_t *rgb) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;
    int o = (y * W + x) * 3;
    float3 c = make_float3(core[o + 0], core[o + 1], core[o + 2]);
    float3 g = sample_bilinear(glow, GW, GH, (x + 0.5f) * 0.25f, (y + 0.5f) * 0.25f);
    float r = 1.0f - expf(-(c.x + g.x * 1.5f) * 1.25f);
    float gg = 1.0f - expf(-(c.y + g.y * 1.5f) * 1.25f);
    float b = 1.0f - expf(-(c.z + g.z * 1.5f) * 1.25f);

    float ny = (y + 0.5f) / (float)H;
    float hx = (ny - 0.90f) / 0.08f;
    float horizon = expf(-hx * hx);
    r += 0.003f + 0.020f * (1.0f - ny) + 0.010f * horizon;
    gg += 0.006f + 0.026f * (1.0f - ny) + 0.008f * horizon;
    b += 0.020f + 0.050f * (1.0f - ny) + 0.012f * horizon;

    uint32_t hs = hash32((uint32_t)x * 73856093U ^ (uint32_t)y * 19349663U);
    if ((hs & 0xFFFFU) < 5U) {
        float s = 0.55f + 0.45f * ((hs >> 16) & 0xFFFFU) / 65535.0f;
        r += s; gg += s; b += s;
    }
    rgb[o + 0] = (uint8_t)(fminf(fmaxf(r, 0.0f), 1.0f) * 255.0f);
    rgb[o + 1] = (uint8_t)(fminf(fmaxf(gg, 0.0f), 1.0f) * 255.0f);
    rgb[o + 2] = (uint8_t)(fminf(fmaxf(b, 0.0f), 1.0f) * 255.0f);
}

__global__ void rgb_to_nv12_kernel(const uint8_t *rgb, uint8_t *nv12, int W, int H) {
    int bx = blockIdx.x * blockDim.x + threadIdx.x;
    int by = blockIdx.y * blockDim.y + threadIdx.y;
    if (bx >= W / 2 || by >= H / 2) return;
    int x = bx * 2, y = by * 2;
    float r = 0.0f, g = 0.0f, b = 0.0f;
    #pragma unroll
    for (int dy = 0; dy < 2; ++dy) {
        #pragma unroll
        for (int dx = 0; dx < 2; ++dx) {
            int p = ((y + dy) * W + (x + dx)) * 3;
            float R = (float)rgb[p + 0], G = (float)rgb[p + 1], B = (float)rgb[p + 2];
            r += R; g += G; b += B;
            float Y = 16.0f + 219.0f * (0.2126f * R + 0.7152f * G + 0.0722f * B) / 255.0f;
            nv12[(y + dy) * W + x + dx] = (uint8_t)fminf(fmaxf(Y, 16.0f), 235.0f);
        }
    }
    r *= 0.25f; g *= 0.25f; b *= 0.25f;
    int uv = H * W + by * W + x;
    float U = 128.0f + 224.0f * (-0.1146f * r - 0.3854f * g + 0.5f * b) / 255.0f;
    float V = 128.0f + 224.0f * (0.5f * r - 0.4542f * g - 0.0458f * b) / 255.0f;
    nv12[uv + 0] = (uint8_t)fminf(fmaxf(U, 16.0f), 240.0f);
    nv12[uv + 1] = (uint8_t)fminf(fmaxf(V, 16.0f), 240.0f);
}

__global__ void debug_bursts_kernel(const BurstState *b, float *out, int B) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i >= B) return;
    BurstState s = b[i];
    float *o = out + i * 11;
    o[0] = s.ox; o[1] = s.oy;
    o[2] = s.cr; o[3] = s.cg; o[4] = s.cb;
    o[5] = s.birth; o[6] = s.life; o[7] = s.z; o[8] = s.decay; o[9] = s.wind;
    o[10] = (float)s.count;
}

struct Renderer {
    int W, H, B, P, GW, GH;
    BurstState *bursts = nullptr;
    ParticleState *parts = nullptr;
    float *core = nullptr, *glow = nullptr, *glow_tmp = nullptr;
    uint8_t *rgb = nullptr;
    float *dbg = nullptr;

    Renderer(int w, int h, int b, int p) : W(w), H(h), B(b), P(p), GW(w / 4), GH(h / 4) {
        cudaMalloc(&bursts, sizeof(BurstState) * B);
        cudaMalloc(&parts, sizeof(ParticleState) * B * P);
        cudaMalloc(&core, sizeof(float) * W * H * 3);
        cudaMalloc(&glow, sizeof(float) * GW * GH * 3);
        cudaMalloc(&glow_tmp, sizeof(float) * GW * GH * 3);
        cudaMalloc(&rgb, sizeof(uint8_t) * W * H * 3);
        cudaMalloc(&dbg, sizeof(float) * B * 11);
        cudaMemset(bursts, 0, sizeof(BurstState) * B);
        cudaMemset(core, 0, sizeof(float) * W * H * 3);
        cudaMemset(glow, 0, sizeof(float) * GW * GH * 3);
        cudaMemset(rgb, 0, sizeof(uint8_t) * W * H * 3);
    }
    ~Renderer() {
        cudaFree(bursts); cudaFree(parts); cudaFree(core);
        cudaFree(glow); cudaFree(glow_tmp); cudaFree(rgb); cudaFree(dbg);
    }
};

static std::unique_ptr<Renderer> g_renderer;

void fw_init(int64_t W, int64_t H, int64_t B, int64_t P) {
    g_renderer = std::make_unique<Renderer>((int)W, (int)H, (int)B, (int)P);
}

void fw_step(torch::Tensor nv12, double t, int64_t frame, bool initial) {
    TORCH_CHECK(g_renderer != nullptr, "fw_init not called");
    TORCH_CHECK(nv12.is_cuda() && nv12.dtype() == torch::kUInt8 && nv12.is_contiguous(), "nv12 must be contiguous CUDA uint8");
    auto *r = g_renderer.get();
    TORCH_CHECK(nv12.numel() >= (int64_t)r->W * r->H * 3 / 2, "nv12 too small");
    uint8_t *out = nv12.data_ptr<uint8_t>();

    update_bursts_kernel<<<r->B, r->P>>>(
        r->bursts, r->parts, r->B, r->P, (float)r->W, (float)r->H, (float)t, (uint32_t)frame, initial);

    int ncore = r->W * r->H * 3, nglow = r->GW * r->GH * 3;
    decay_buffers_kernel<<<(ncore + 255) / 256, 256>>>(r->core, ncore, 0.93f, r->glow, nglow, 0.90f);

    int np = r->B * r->P;
    render_particles_kernel<<<(np + 255) / 256, 256>>>(
        r->bursts, r->parts, r->core, r->glow, r->W, r->H, r->GW, r->GH, r->B, r->P, (float)t);

    dim3 bdim(16, 16);
    dim3 gdim((r->GW + 15) / 16, (r->GH + 15) / 16);
    blur_glow_kernel<<<gdim, bdim>>>(r->glow, r->glow_tmp, r->GW, r->GH);

    dim3 cdim((r->W + 15) / 16, (r->H + 15) / 16);
    composite_kernel<<<cdim, bdim>>>(r->core, r->glow_tmp, r->W, r->H, r->GW, r->GH, r->rgb);

    dim3 yuvdim((r->W / 2 + 15) / 16, (r->H / 2 + 15) / 16);
    rgb_to_nv12_kernel<<<yuvdim, bdim>>>(r->rgb, out, r->W, r->H);
    cudaDeviceSynchronize();
}

void fw_copy_rgb(torch::Tensor rgb) {
    TORCH_CHECK(g_renderer != nullptr, "fw_init not called");
    TORCH_CHECK(rgb.is_cuda() && rgb.dtype() == torch::kUInt8 && rgb.is_contiguous(), "rgb must be contiguous CUDA uint8");
    auto *r = g_renderer.get();
    cudaMemcpy(rgb.data_ptr<uint8_t>(), r->rgb, (size_t)r->W * r->H * 3, cudaMemcpyDeviceToDevice);
}

void fw_copy_bursts(torch::Tensor out) {
    TORCH_CHECK(g_renderer != nullptr, "fw_init not called");
    TORCH_CHECK(!out.is_cuda() && out.dtype() == torch::kFloat32 && out.is_contiguous(), "out must be contiguous CPU float32");
    auto *r = g_renderer.get();
    TORCH_CHECK(out.numel() >= (int64_t)r->B * 11, "out too small");
    debug_bursts_kernel<<<(r->B + 31) / 32, 32>>>(r->bursts, r->dbg, r->B);
    cudaMemcpy(out.data_ptr<float>(), r->dbg, sizeof(float) * r->B * 11, cudaMemcpyDeviceToHost);
}

void fw_free() { g_renderer.reset(); }
'''

cpp_src = r'''
#include <torch/extension.h>
void fw_init(int64_t W, int64_t H, int64_t B, int64_t P);
void fw_step(torch::Tensor nv12, double t, int64_t frame, bool initial);
void fw_copy_rgb(torch::Tensor rgb);
void fw_copy_bursts(torch::Tensor out);
void fw_free();
'''

print("compiling 4K CUDA fireworks renderer...")
ext = load_inline(
    name="fireworks_cuda_ext",
    cpp_sources=cpp_src,
    cuda_sources=cuda_src,
    functions=["fw_init", "fw_step", "fw_copy_rgb", "fw_copy_bursts", "fw_free"],
    extra_cuda_cflags=["-O3", "--use_fast_math"],
    verbose=False,
)
print("compiled")


class FireworksCuda:
    def __init__(self, width=3840, height=2160, bursts=32, particles=256):
        self.W, self.H = width, height
        self.B, self.P = bursts, particles
        self.nv12 = torch.empty((height * 3 // 2, width), dtype=torch.uint8, device="cuda")
        self.frame = 0
        ext.fw_init(width, height, bursts, particles)
        self.initial = True

    def step(self, t):
        ext.fw_step(self.nv12, float(t), self.frame, self.initial)
        self.initial = False
        self.frame += 1
        return self.nv12

    def rgb(self):
        out = torch.empty((self.H, self.W, 3), dtype=torch.uint8, device="cuda")
        ext.fw_copy_rgb(out)
        return out

    def bursts(self):
        out = torch.empty((self.B, 11), dtype=torch.float32)
        ext.fw_copy_bursts(out)
        return out

    def close(self):
        ext.fw_free()


if __name__ == "__main__":
    fw = FireworksCuda()
    # warm up
    for i in range(80):
        fw.step(i / 25.0)
    t0 = time.perf_counter()
    nv = fw.step(4.0)
    torch.cuda.synchronize()
    print("one step ms", (time.perf_counter() - t0) * 1000)
    rgb = fw.rgb()
    from PIL import Image
    Image.fromarray(rgb.cpu().numpy()).save("fireworks_frame.png")
    print("saved fireworks_frame.png")
