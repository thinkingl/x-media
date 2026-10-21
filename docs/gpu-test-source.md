# GPU 连续测试码流（GPU Test Source）详细方案

> 状态：**已落地并连续运行，等待 KDM 286 最终预览确认** ｜ 版本：v1.0 ｜ 日期：2026-10-21
> 相关代码：`server/internal/media/rtsp_input.go`（PTS 修复）、`server/internal/media/rtsp_input_pts_test.go`
> 当前生成器：`/tmp/opencode/gpu_live.py`（完整脚本见附录 A）
> 当前 supervisor：`/tmp/opencode/gpu_live_supervisor.sh`（附录 B）

---

## 0. 当前部署速查

| 项目 | 当前值 |
|---|---|
| 主机 / 网卡 | `192.168.88.102`（NAT 后 `172.16.194.145`） |
| x-media API | `http://127.0.0.1:18090`，dev key `dev-api-key` |
| x-media RTSP server | `:5544` |
| 原始测试流 | `rtsp://192.168.88.102:5544/gpu_test` |
| x-media 测试输出 | `rtsp://192.168.88.102:5544/live/78ac5050-a745-b6f6-a83b-5f5f3b88e6ce` |
| 测试输入 | `9a1f1a00-6476-6250-60e4-bb3fba5482dd`（`GPU Test Source`） |
| 测试输出 | `78ac5050-a745-b6f6-a83b-5f5f3b88e6ce`（`GPU Test RTSP Out`） |
| 测试管道 | `b9cd2cbb-9045-dec3-34c8-faa84558ea95` |
| KDM 286 临时接入 | 设备 `32000000001120000286` / 通道 `32000000001320003627`（`x-test`） |
| 286 管道 | `f5a25350-0884-d1f1-563a-db4a6d4bbdb2`：`9a1f…` → `85211b63-272c-0c18-50b8-4840a51209d6` |
| 286 原输入 | `36f9dc0f-9eba-8f3f-b192-5b851698238d`（`kdm-测试源`，仍 running，可随时切回） |
| KDM 平台 | `172.16.240.76:33001`，SIP 域 `3200000000`，平台 ID `32000000002000000000` |

> 286 的切换是**临时测试**，287/288/289 未动。切换/回退方法见 §6.4。

---

## 1. 目标与需求映射

目标：**持续**产生一路可被 x-media / GB28181 平台消费的测试码流，用于实时预览、延迟测量、冻结/花屏/帧序排查。

| 需求 | 实现 |
|---|---|
| 低 CPU | GPU 生成 NV12 + NVENC 零拷贝编码；PyTorch 侧约 11% 单核，推流 ffmpeg 约 1.2% |
| 图像随机性 | 每帧 6 个随机位置/大小/颜色的矩形 + 可选随机噪声 + 移动的亮条 |
| 毫秒级时间戳 | 画面左上角烧录 `HH:MM:SS.mmm`，另带 `Fxxxxxx` 帧号；时间取生成时 wall clock |
| 利用 GPU | torch CUDA 生成 NV12，PyNvVideoCodec 直接从 CUDA 显存编码，无 CPU 回传 |
| 持续运行 | supervisor 循环拉起；x-media 重启后推流自动重连；当前已连续运行数万帧 |
| 兼容性 | H.264 High、1080p25、8 Mbps CBR、无 B 帧、GOP 25，适合 GB28181/WebRTC/RTSP 预览 |

---

## 2. 方案对比与选型

在 1080p25 实时（`-re`/wall-clock 节流）下实测，CPU 为**单核百分比**（机器 4 核）：

| 方案 | 生成方式 | 编码 | CPU（单核） | GPU SM | NVENC | 结论 |
|---|---|---|---|---|---|---|
| A | ffmpeg lavfi `testsrc2 + noise + drawtext` | `h264_nvenc` | 16% | 1.0% | 9.1% | 最简单；生成/叠加仍在 CPU |
| B | PyTorch CUDA 随机帧 → CPU rawvideo pipe | `h264_nvenc` | 26.1% | 2.9% | 7.8% | GPU 生成被 CPU 回传抵消，CPU 反而更高 |
| **C（当前）** | **PyTorch CUDA NV12 + GPU 毫秒叠加，零拷贝** | **PyNvVideoCodec / NVENC** | **11.0% + 1.2%（推流）** | **1.0%** | **8.4%** | **选型：低 CPU + 全 GPU 生成/编码** |
| D | 自定义 C/CUDA + NVENC SDK 零拷贝 | NVENC | 未实现（预计更低） | — | — | 生产级可选，开发量大 |

补充：

- 方案 A 的 4K25 实测：CPU **47%**、GPU SM 5.5%、NVENC 35.5%；分辨率升高后 CPU 生成/叠加成为瓶颈，C 的 GPU 生成更有优势。
- GStreamer `gltestsrc` 可用，但 Ubuntu 的 `gstreamer1.0-plugins-bad` **不含 `nvh264enc`**，且本机无头 GL 初始化失败，暂不作为方案。
- 方案 A 依赖 `drawtext`；本机 `/usr/local/bin/ffmpeg`（9.0.2 源码编译）未启用 freetype，`/usr/bin/ffmpeg`（4.4.2）才有 `drawtext`。方案 C 不依赖 drawtext，故不受影响。

**选型结论：C。** 对 1080p25 单路，C 与 A 的 CPU 差距不大，但 C 把随机图案和毫秒叠加都放在 GPU，分辨率/路数升高时扩展性更好，且不需要重建 ffmpeg 的 freetype 支持。

---

## 3. 总体架构

```
┌──────────────────────────────┐
│ PyTorch CUDA（GTX 1060）      │
│  NV12 单块显存：Y + UV        │
│  随机矩形/噪声/移动条         │
│  5x7 字体烧录 ms 时钟+帧号    │
└──────────────┬───────────────┘
               │ __cuda_array_interface__（零拷贝）
┌──────────────▼───────────────┐
│ PyNvVideoCodec / NVENC       │
│  H.264 High, 1080p25, 8Mbps  │
│  CBR, GOP 25, bf=0           │
└──────────────┬───────────────┘
               │ Annex-B H.264（stdout）
┌──────────────▼───────────────┐
│ ffmpeg -c:v copy             │
│  -f rtsp -rtsp_transport tcp │
└──────────────┬───────────────┘
               │ rtsp://127.0.0.1:5544/gpu_test
┌──────────────▼───────────────┐
│ x-media RTSP server (:5544)  │
│  OnAnnounce 接受任意 path    │
└───────┬───────────────┬──────┘
        │               │
        │ x-media RTSP input 9a1f…（tcp, 自动重连）
        │               │
        ▼               ▼
  pipe b9cd…       pipe f5a25350…
        │               │
        ▼               ▼
 RTSP 输出 78ac…   GB28181 输出 85211…（KDM 286）
 本地预览          平台预览 / 延迟测量
```

关键点：

1. **生成与编码都在 GPU**：NV12 缓冲区只分配一次，Y/UV 是同一块显存的视图，NVENC 直接消费；CPU 不参与逐像素生成或回传。
2. **x-media 只做转发/封装**：RTSP input 解析 RTP/H.264 → 标准帧 → pipe → RTSP/GB28181 sink；不转码。
3. **一路输入可 fan-out**：同一个测试输入同时给本地 RTSP 输出和 286 GB28181 输出，互不影响。
4. **286 的 GB28181 sink 仍按现网配置工作**：平台注册、目录、心跳、PS/RTP 推流全走既有链路；测试源只是替换了 286 的输入。

---

## 4. 环境与依赖

### 4.1 硬件/驱动

| 项 | 值 |
|---|---|
| GPU | NVIDIA GeForce GTX 1060 6GB（Pascal） |
| 驱动 | 535.309.01（NVENC API 12.1） |
| CUDA | 12.2 runtime / 12.9 toolkit（`nvcc`） |
| NVENC | H.264/HEVC；无 AV1（Pascal） |

### 4.2 软件版本

| 组件 | 版本/路径 | 说明 |
|---|---|---|
| Python | 3.10.12 | 系统 Python |
| PyTorch | 2.9.1+cu126 | 已有，CUDA 可用 |
| PyNvVideoCodec | 2.2.3 | 当前安装在 `/tmp/opencode/nvsite`（非持久） |
| ffmpeg（推流） | `/usr/local/bin/ffmpeg` 9.0.2 | 支持 `h264_nvenc`/`cuvid`；无 drawtext |
| ffmpeg（方案 A 备选） | `/usr/bin/ffmpeg` 4.4.2 | 有 testsrc2/noise/drawtext |
| x-media | 当前 `bin/x-media-server`（含 PTS 修复 `08fe39d`） | API `:18090`、RTSP `:5544` |

### 4.3 安装 PyNvVideoCodec（当前临时方式）

```bash
mkdir -p /tmp/opencode/nvsite
python3 -m pip install --no-input --no-deps \
  --target /tmp/opencode/nvsite PyNvVideoCodec==2.2.3
```

运行时：

```bash
PYTHONPATH=/tmp/opencode/nvsite python3 /tmp/opencode/gpu_live.py --help
```

> `/tmp` 在重启后可能被清理。长期使用请改为 `pip install --user PyNvVideoCodec` 或独立 venv，并同步修改 supervisor 的 `PYTHONPATH`。

---

## 5. 生成器详细设计

### 5.1 NV12 单块显存与零拷贝

PyNvVideoCodec 的 GPU 输入要求 **U plane 紧跟在 Y plane 之后**，否则报：

```
Unsupported surface allocation. u plane must follow yplane.
```

因此只分配一块 `H*3/2 x W` 的 uint8 显存，再切视图：

```python
buf = torch.empty((h * 3 // 2, w), dtype=torch.uint8, device="cuda")
y  = buf[:h].view(h, w, 1)          # (H, W, 1)
uv = buf[h:].view(h // 2, w // 2, 2) # (H/2, W/2, 2)，U/V 交错
```

传给编码器时，包一层 `cuda()` 返回 `__cuda_array_interface__` 的对象：

```python
class CudaFrame:
    def __init__(self, tensor): self.t = tensor
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
    def __init__(self, planes): self.planes = planes
    def cuda(self): return [CudaFrame(p) for p in self.planes]
```

说明：

- 直接传 `torch.Tensor` 会触发 `Tensor.__dlpack__() takes 1 positional argument but 2 were given`（绑定层按位置传 `stream`），所以用 `__cuda_array_interface__` 包装。
- 两级 `cuda()` 返回的对象必须是**带 `__cuda_array_interface__` 的对象**，不能直接返回 dict。
- Y 形状 `(H,W,1)`，UV 形状 `(H/2,W/2,2)`；不要传两个独立分配。

### 5.2 随机图案

当前默认 `--pattern mixed`：

1. 背景填充 `Y=24`、`UV=128`（深灰）。
2. 每帧生成 6 个随机矩形：
   - 位置/宽高随机；
   - Y 亮度随机（50~235）；
   - U/V 色度随机（80~175），形成彩色块。
3. 叠加幅度可配的随机噪声（`--noise-amp 12`）；`uint8` 加完 `clamp(16,235)`。
4. 画移动竖条（宽 80，`x=(i*12)%W`）和横条（高 60，`y=(i*9)%H`），用于检测冻结、卡顿和帧序。
5. 左上角烧录时间戳/帧号（见 §5.3）。

可选模式：

| `--pattern` | 行为 | 适用 |
|---|---|---|
| `mixed`（默认） | 随机矩形 + 噪声 + 移动条 | 兼顾随机性、可压缩性和视觉可读性 |
| `noise` | 全画面随机 Y/UV | 最大随机性；码率高、画面噪点多 |
| `blocks` | 随机矩形 + 移动条，无噪声 | 低码率、块状随机性 |

> 全画面均匀噪声几乎不可压缩。目标码率设得过低时，NVENC 受最小 QP 限制可能明显超码率；测试带宽时优先用 `mixed`/`blocks`，或降低 `--noise-amp`。

### 5.3 毫秒时间戳叠加

- 5x7 点阵字体，字符集 `0123456789:.F`；
- 第一行 `HH:MM:SS.mmm`（12 字符，wall clock，毫秒）；
- 第二行 `F%06d` 帧号（用于检查是否丢帧/冻结）；
- 黑色底框 + 白色文字；底框区域 UV 置 128，避免彩色噪声影响可读性；
- 字体缩放自动 `max(2, H//240)`：1080p → 4，720p → 3；
- 文本渲染完全用 torch 张量索引/拼接完成，不上传 CPU。

示例（1080p 实际拉流截帧）：

```
12:21:35.878
F001189
```

时间戳取**生成时刻**的 wall clock，因此平台预览画面上的时间与真实时间之差可直接用于端到端延迟评估。PTS 本身是 CFR 25fps（40ms 间隔），与画面上的毫秒时钟是两套信息：前者用于媒体同步，后者用于人眼/截图测延迟。

### 5.4 编码参数

```python
enc = nvc.CreateEncoder(
    w, h, "NV12", False,          # False = GPU/device memory 输入
    codec="h264",
    preset="P4",                  # 必须大写！小写 p4 会构造失败
    fps=25,
    rc="cbr",
    bitrate=8_000_000,            # bps
    maxbitrate=8_000_000,
    vbvbufsize=8_000_000,
    gop=25,
    bf=0,                         # 无 B 帧：PTS=DTS，GB28181/WebRTC 友好
)
```

参数说明：

| 参数 | 值 | 说明 |
|---|---|---|
| profile | High（默认） | 1080p25 输出 `profile=High, level=4.0` |
| CBR | 8 Mbps | 实测稳定 8.0~8.1 Mbps |
| GOP | 25 | 1 秒一个 IDR，平台预览/新 reader 快速出图 |
| B 帧 | 0 | 避免 GB28181/WebRTC 对 DTS 重排的依赖 |
| 输入格式 | NV12 | GPU 常驻，零拷贝 |

> `bitrate` 单位是 **bps**（文档与实测一致）；短时全噪声编码可能因关键帧/最小 QP 超目标，不代表单位错误。`preset` 必须用 `P1`~`P7` 大写。

### 5.5 节流、输出与统计

- 以 `time.perf_counter()` 做单调节流，目标 `1/fps`；
- 落后超过 0.5s 时重置调度债务，避免突发补帧；
- 每帧把 NVENC 返回的 Annex-B 包写入 stdout（或 `--out` 文件），stdout 每帧 `flush()`；
- 每 5 秒向 stderr 打印：输入帧号、编码包数、实际 fps、码率、进程 CPU；
- 收到 SIGINT/SIGTERM 时退出循环，调用 `EndEncode()` 冲刷尾部包。

输出是**裸 H.264 Annex-B**（首包以 `00 00 00 01 67 ...` 开头），所以可以直接：

```bash
# 离线文件
PYTHONPATH=/tmp/opencode/nvsite python3 gpu_live.py \
  --duration 5 --out /tmp/opencode/test.h264 --mbps 8 --pattern mixed

ffprobe -f h264 /tmp/opencode/test.h264
```

也可以接 ffmpeg 做 RTSP 推流。

### 5.6 命令行参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--width/--height` | 1920/1080 | 必须为偶数（NV12） |
| `--fps` | 25 | 帧率 |
| `--gop` | 25 | IDR 间隔（帧） |
| `--mbps` | 8.0 | 目标码率（Mbit/s），内部换算为 bps |
| `--rc` | cbr | `cbr` / `vbr` |
| `--pattern` | mixed | `mixed` / `noise` / `blocks` |
| `--noise-amp` | 12 | mixed 模式噪声幅度（0 关闭） |
| `--duration` | 0 | 0 = 无限运行 |
| `--out` | `-` | `-` = stdout，否则写 `.h264` 文件 |
| `--no-overlay` | 关 | 关闭时间戳/帧号叠加 |
| `--scale` | 0 | 字体缩放，0 = 按高度自动 |
| `--stats` | 5.0 | 统计打印间隔（秒） |

---

## 6. 推流与 x-media 接入

### 6.1 ffmpeg RTSP 推流

supervisor 中的实际命令：

```bash
python3 /tmp/opencode/gpu_live.py --mbps 8 --pattern mixed --duration 0 2>>gen.log | \
  /usr/local/bin/ffmpeg -hide_banner -v warning -fflags +genpts -r 25 \
    -f h264 -i pipe:0 -c:v copy -muxdelay 0 -muxpreload 0 \
    -f rtsp -rtsp_transport tcp rtsp://127.0.0.1:5544/gpu_test \
    >>pub.log 2>&1
```

- `-r 25`：裸 H.264 没有容器时间戳，用输入帧率生成 PTS；
- `-c:v copy`：不转码，CPU 只做 RTP 打包；
- `-muxdelay 0 -muxpreload 0`：降低推流启动延迟；
- 首次可能打印一次 `Timestamps are unset in a packet for stream 0`；这是首包警告，实测 RTP 时间戳按 40ms 递增，x-media input 的 PTS 修复后不受影响。
- x-media 的 RTSP server `OnAnnounce` 接受任意 path，无需预先创建输出；推流路径 `gpu_test` 在 publisher 断开后会被删除，重连后重建。

### 6.2 x-media 测试输入/输出/管道

当前对象：

| 对象 | ID | 关键配置/状态 |
|---|---|---|
| 输入 | `9a1f1a00-6476-6250-60e4-bb3fba5482dd` | `rtsp://127.0.0.1:5544/gpu_test`，`transport=tcp`，`reconnect=true`，1000ms；running |
| 输出 | `78ac5050-a745-b6f6-a83b-5f5f3b88e6ce` | RTSP server `:5544`；running |
| 管道 | `b9cd2cbb-9045-dec3-34c8-faa84558ea95` | 输入 → 输出；running，`dropped=0` |

创建命令（换新环境时）：

```bash
API=http://127.0.0.1:18090/api/v1
KEY=dev-api-key

# 1) 输入
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"name":"GPU Test Source","type":"rtsp","config":"{\"url\":\"rtsp://127.0.0.1:5544/gpu_test\",\"transport\":\"tcp\",\"reconnect\":true,\"reconnect_interval_ms\":1000}"}' \
  $API/inputs

# 2) RTSP 测试输出（与现有 RTSP sink 共用 :5544）
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"name":"GPU Test RTSP Out","type":"rtsp","config":"{\"mode\":\"server\",\"addr\":\":5544\"}"}' \
  $API/outputs

# 3) 管道（用上一步返回的 id）
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"input_id":"<INPUT_ID>","output_id":"<OUTPUT_ID>"}' \
  $API/pipes

# 4) 依次启动 output / input / pipe
curl -s -X POST -H "X-API-Key: $KEY" $API/outputs/<OUTPUT_ID>/start
curl -s -X POST -H "X-API-Key: $KEY" $API/inputs/<INPUT_ID>/start
curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/<PIPE_ID>/start
```

启动顺序建议：**output → input → pipe**。输出先建立 RTSP server path；输入再拉 publisher；最后接 pipe。

### 6.3 本地预览/验证

```bash
# 元数据
ffprobe -v error -rtsp_transport tcp \
  -show_entries stream=codec_name,width,height,r_frame_rate,avg_frame_rate \
  rtsp://192.168.88.102:5544/live/78ac5050-a745-b6f6-a83b-5f5f3b88e6ce

# 播放
ffplay -rtsp_transport tcp \
  rtsp://192.168.88.102:5544/live/78ac5050-a745-b6f6-a83b-5f5f3b88e6ce

# 20 秒解码健康检查（期望 errors=0，dup_frames=0，drop_frames=0）
ffmpeg -hide_banner -v error -rtsp_transport tcp \
  -i rtsp://192.168.88.102:5544/live/78ac5050-a745-b6f6-a83b-5f5f3b88e6ce \
  -t 20 -f null - -progress /tmp/prog.txt 2>/tmp/err.log
grep -c 'error while decoding' /tmp/err.log
tail -5 /tmp/prog.txt
```

### 6.4 KDM 286（x-test）临时接入

286 原管道 `f5a25350-0884-d1f1-563a-db4a6d4bbdb2` 原本是：

```
36f9dc0f…（kdm-测试源，test.mp4） -> 85211b63…（kdm-GB28181(x-test)）
```

当前临时改为：

```
9a1f1a00…（GPU Test Source） -> 85211b63…（kdm-GB28181(x-test)）
```

切换步骤（运行中的 pipe 不能直接改，先停）：

```bash
API=http://127.0.0.1:18090/api/v1; KEY=dev-api-key
PIPE=f5a25350-0884-d1f1-563a-db4a6d4bbdb2

curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/$PIPE/stop

curl -s -X PUT -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"input_id":"9a1f1a00-6476-6250-60e4-bb3fba5482dd","output_id":"85211b63-272c-0c18-50b8-4840a51209d6"}' \
  $API/pipes/$PIPE

curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/$PIPE/start
```

注意：停止 pipe 后，输出 286 会因为“无管道引用”被 media hub 移除并重新创建；GB28181 sink 会重新注册（当前日志 12:27:01 重新注册成功，12:28:01 心跳 OK）。这是既有行为，不是测试源引入的问题。

**回退到原 test.mp4：**

```bash
curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/$PIPE/stop
curl -s -X PUT -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"input_id":"36f9dc0f-9eba-8f3f-b192-5b851698238d","output_id":"85211b63-272c-0c18-50b8-4840a51209d6"}' \
  $API/pipes/$PIPE
curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/$PIPE/start
```

平台侧请手动对 **286 / 通道 32000000001320003627** 发起预览。预期画面：深色背景、随机彩色块、白色移动条、左上角毫秒时钟和帧号。

---

## 7. 时间戳、PTS 与本次修复

### 7.1 三层时间信息

| 层 | 精度/单位 | 用途 |
|---|---|---|
| 画面烧录时钟 | 毫秒，wall clock | 人眼/截图测端到端延迟 |
| 帧号 `F%06d` | 帧 | 检测冻结、丢帧、重复 |
| RTP/媒体 PTS | 90 kHz，CFR 40ms | 播放器同步、GB28181 PS 封装 |

生成器输出裸 H.264，ffmpeg 用 `-r 25` 生成 CFR PTS；x-media RTSP input 从 RTP 时间戳恢复 PTS；GB28181 sink 再按 90kHz 写入 PS/PES。测试源无 B 帧，PTS=DTS，避免重排问题。

### 7.2 已修复：大关键帧分片导致 PTS 全为 0

**现象**（接入初期）：

- x-media RTSP 测试输出 `r_frame_rate=50/1`，`avg_frame_rate=25/1`；
- RTSP sink 日志 `type=0 pts=0`；
- 拉流出现 `Non-monotonic DTS`、`error while decoding MB ...`；
- 直连 publisher 的 `gpu_test` 却是 25fps、PTS 正常。

**根因**：

`rtsp_input.go` 原来在 `dec.Decode(pkt)` **之后**调用 `PacketPTS`。对于 1080p 大 IDR，RTP 层用多个 **FU-A** 分片；组装完成 AU 的最后一个分片是 FU-A 结束分片，`PTSEqualsDTS()` 返回 false。gortsplib 的 `rtptime.GlobalDecoder` 只在 `PTSEqualsDTS` 的包上初始化（IDR/SPS/PPS 或 FU-A 起始分片），因此永远不初始化，所有帧 PTS=0。

**修复**：

- 在 `dec.Decode(pkt)` **之前**调用 `PacketPTS`，让 IDR 起始分片完成初始化；
- 若 `ptsOK=false`（中途接入 GOP、尚未初始化），丢弃该 AU，避免把 PTS=0 传给下游；
- 音频路径不变。

核心代码：

```go
// 必须在 Decode 之前：timeDecoder 只在 PTSEqualsDTS 的包上初始化
// （IDR/SPS/PPS，或 FU-A 起始分片）。
pts, ptsOK := r.client.PacketPTS(medi, pkt)

nalus, err := dec.Decode(pkt)
// ... 组装 Annex-B AU ...

if !ptsOK {
    logger.Debugf("RTSP input drop frame before PTS init [%s] ch=%d ts=%d",
        r.id, channelID, pkt.Timestamp)
    return
}
```

**回归测试**：`server/internal/media/rtsp_input_pts_test.go::TestRTSPInput_PTSForFragmentedIDR`

- 用真实 `RTSPSink` + `RTSPInput`；
- 发送 100 KB 的单 IDR NAL（必然 FU-A 分片），SPS/PPS 只走 SDP；
- 旧实现：所有帧 PTS=0，测试失败；
- 新实现：首帧 PTS=0（timeDecoder 从 0 起算），后续递增，测试通过。

**提交/验证**：

- commit `08fe39d`（已 push）：`fix(rtsp): 大关键帧分片时 PTS 始终为 0 —— 在 Decode 前初始化 RTP 时间解码器`
- `go test ./... -count=1` 全绿（media 包约 260s）；
- 新二进制已部署；286 于 12:27:01 重新注册，12:28:01 心跳 OK；287/288/289 不受影响。

> 该修复对所有“大关键帧 + FU-A 分片 + SPS/PPS 仅在 SDP”的 RTSP 输入都有效，不限于本测试源。

---

## 8. 性能与稳定性实测

### 8.1 当前方案 C（1080p25，8 Mbps）

| 指标 | 实测 |
|---|---|
| 生成器 CPU | **10.8~11.0%**（单核） |
| ffmpeg 推流 CPU | **1.2%**（单核） |
| 合计 | **约 12.2% 单核**（4 核机器约 3%） |
| GPU SM | 1.0%（随机填充是带宽型轻负载） |
| NVENC | **8.4%**（max 10%） |
| GPU 显存 | 160 MiB |
| Python RSS | 约 731 MB（torch + CUDA context） |
| 编码 | H.264 High，level 4.0，yuv420p，25fps，8.0~8.1 Mbps |
| 稳定性 | 连续数万帧；20s 拉流 0 解码错误、0 dup/drop、speed 1.05x |

### 8.2 与其它方案/分辨率对比

| 场景 | CPU（单核） | GPU SM | NVENC |
|---|---|---|---|
| A：ffmpeg lavfi 1080p25 | 16% | 1.0% | 9.1% |
| A：ffmpeg lavfi 4K25 | 47% | 5.5% | 35.5% |
| B：torch→CPU pipe 1080p25 | 26.1% | 2.9% | 7.8% |
| **C：GPU 零拷贝 1080p25** | **12.2%（含推流）** | **1.0%** | **8.4%** |

> C 的 4K 尚未实测；从架构上看，生成/叠加在 GPU，CPU 增长主要来自 Python/编码调用，预期明显优于 A 的 47%。需要 4K 时建议先跑一轮 `--width 3840 --height 2160` 验证 NVENC/驱动上限。

### 8.3 资源解释

- `utilization.gpu`（SM）只有 1%，不代表 GPU 没用：随机填充是显存带宽型，主要负载在 **NVENC 独立引擎**（8.4%）。
- 单路 1080p25 远未压满 GTX 1060；如要提高 GPU 利用率，可：
  - 提高分辨率/帧率；
  - 多路生成器（每个进程独立 NVENC session）；
  - 增加更复杂的 GPU 图案（Perlin/plasma/多图层）。
- Python RSS 731MB 主要来自 torch/CUDA context；若需要极低内存，可改用方案 D（C/CUDA + NVENC）。

---

## 9. 运行/运维手册

### 9.1 启动

当前使用 nohup supervisor：

```bash
nohup bash /tmp/opencode/gpu_live_supervisor.sh >/dev/null 2>&1 &
```

supervisor 循环：

1. 启动 `gpu_live.py`；
2. 通过管道把 Annex-B 交给 ffmpeg；
3. ffmpeg 推 `rtsp://127.0.0.1:5544/gpu_test`；
4. 任一进程退出 → 2 秒后重启整条 pipeline。

### 9.2 日志与健康检查

| 文件 | 内容 |
|---|---|
| `/tmp/opencode/gen.log` | 生成器每 5s 统计 + 异常 |
| `/tmp/opencode/pub.log` | ffmpeg 推流警告/错误 |
| `/tmp/opencode/supervisor.log` | 每次启动/退出记录 |

```bash
pgrep -af 'gpu_live.py|ffmpeg.*gpu_test'
tail -3 /tmp/opencode/gen.log
tail -3 /tmp/opencode/pub.log
tail -5 /tmp/opencode/supervisor.log

# x-media 侧
curl -s -H 'X-API-Key: dev-api-key' \
  http://127.0.0.1:18090/api/v1/pipes/b9cd2cbb-9045-dec3-34c8-faa84558ea95

# GPU
nvidia-smi --query-gpu=utilization.gpu,utilization.encoder,memory.used \
  --format=csv,noheader,nounits -l 1
```

x-media 日志关键字：

- `RTSP publisher connected: gpu_test`
- `pipe heartbeat [...] written=... dropped=0`
- `RTSP sink WriteFrame #... pts=...`（测试 sink 的 PTS 应持续递增，非 0）
- `RTSP input started` / `RTSP input play OK`

### 9.3 停止/重启

```bash
# 停止 supervisor 及其子进程
pkill -f gpu_live_supervisor.sh
pkill -f 'gpu_live.py --mbps 8'
pkill -f 'ffmpeg.*rtsp://127.0.0.1:5544/gpu_test'

# 重新启动
nohup bash /tmp/opencode/gpu_live_supervisor.sh >/dev/null 2>&1 &
```

> x-media 重启时，publisher 的 TCP 连接会断开，ffmpeg 退出，supervisor 2 秒后重连。实测 x-media 重启后，测试 input/output/pipe **不一定会自动恢复**（本次重启后为 stopped，需要手动 start）；原始 `gpu_test` 推流路径在 x-media 恢复后由 supervisor 自动重建。

### 9.4 持久化建议（尚未实施）

当前脚本/Python 包都在 `/tmp`，**重启机器不会自启**。建议：

1. 把 `gpu_live.py`、`gpu_live_supervisor.sh` 移到仓库 `tools/gpu-test-source/`（或 `/opt/x-media-gpu-test/`）；
2. `pip install --user PyNvVideoCodec==2.2.3`（或 venv），去掉 `/tmp` 的 `PYTHONPATH`；
3. 用 systemd user service 常驻：

```ini
[Unit]
Description=x-media GPU test source
After=network-online.target

[Service]
Type=simple
Environment=PYTHONPATH=%h/.local/lib/python3.10/site-packages
ExecStart=%h/code/x-media/tools/gpu-test-source/supervisor.sh
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now x-media-gpu-test.service
loginctl enable-linger "$USER"   # 需要权限；否则登录后才启动
```

---

## 10. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| `Preset p4 not found` / 构造失败 | preset 必须大写 | 用 `P4`/`P1`/`P7` |
| `u plane must follow yplane` | Y/UV 分开分配 | 用单块 NV12 buffer + 视图 |
| `Tensor.__dlpack__() takes 1 positional...` | 绑定层按位置传 stream | 包 `__cuda_array_interface__` |
| `Invalid shape: [H,W], expected [H,W,1]` | 平面少了通道维 | Y `(H,W,1)`，UV `(H/2,W/2,2)` |
| `Unknown filter 'drawtext'` | 9.0.2 未启用 freetype | 方案 A 用 `/usr/bin/ffmpeg`；方案 C 不需要 |
| x-media 输出 50fps / PTS=0 / DTS 非单调 | 旧二进制，FU-A 分片 PTS bug | 部署含 `08fe39d` 的二进制；跑 `TestRTSPInput_PTSForFragmentedIDR` |
| `Timestamps are unset in a packet` | 裸 H.264 首包无 PTS | 单次警告可忽略；PTS 由 RTP 时间戳恢复 |
| KDM 286 无画面 | pipe/input 未 running；平台未重新预览；无音频；编码不兼容 | 查 pipe/input 状态、`RTSP input play OK`、KDM catalog/keepalive；重新发起预览；先用本地 RTSP URL 确认源正常 |
| x-media 重启后测试 input/output stopped | 本次观察到的行为 | 通过 API/UI 重新 start；原始 publisher 不受影响 |
| 码率远超 `--mbps` | 全噪声 + 低目标 + 最小 QP | 用 `mixed`/`blocks`，降低噪声幅度或提高目标 |
| `PyNvVideoCodec` import 失败 | 未安装/未设 PYTHONPATH | `PYTHONPATH=/tmp/opencode/nvsite` 或持久安装 |
| GPU 显存/进程持续增长 | 通常不会；检查是否频繁重建 encoder | 观察 RSS/GPU mem；supervisor 只在进程退出时重建 |

---

## 11. 风险、边界与后续

**已知边界：**

- 当前测试流**视频-only**，没有音频；GB28181 平台若强制要求音频，需要额外加 1 kHz 测试音或静音 AAC。
- 画面时钟是 wall clock，不是 NTP 同步源；跨机测延迟时需保证主机时间准确。
- PTS 是 CFR 25fps；若需要逐帧 wall-clock/VFR PTS，需要自定义 muxer（PyAV/自写 RTP），当前未实现。
- 生成器/Python 包在 `/tmp`，不具备重启自启；测试 input/output/pipe 是 x-media 运行时 DB 对象，重启后可能需要手动 start。
- 286 当前是临时切到测试源；KDM 预览结果待确认，确认后可保留或回退。

**后续可选项：**

1. **加音频**：在 ffmpeg 推流侧混入 1 kHz `sine` 或 `anullsrc`，`-c:a aac`；注意音视频两路实时源的漂移。
2. **持久化**：脚本进仓库 + `pip install --user` + systemd user service。
3. **VFR 毫秒 PTS**：用 PyAV 或自写 RTP/PS 封装，把生成时刻的 ms 直接写入 PTS。
4. **方案 D**：C/CUDA + NVENC SDK，进一步降低 CPU/RSS，适合多路/4K 生产级测试源。
5. **原生集成**：在 x-media 里增加 `synthetic` 输入类型，由 Go 管理子进程/RTSP，不依赖外部 supervisor。

---

## 附录 A：完整生成器脚本（`gpu_live.py`）

```python
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

try:
    import torch
    import PyNvVideoCodec as nvc
except ImportError:
    # Local research install used during development.
    sys.path.insert(0, "/tmp/opencode/nvsite")
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
```

## 附录 B：supervisor 脚本

```bash
#!/usr/bin/env bash
set -u
export PYTHONPATH=/tmp/opencode/nvsite
while true; do
  echo "[supervisor] start $(date '+%F %T')" >> /tmp/opencode/supervisor.log
  python3 /tmp/opencode/gpu_live.py --mbps 8 --pattern mixed --duration 0 \
      2>>/tmp/opencode/gen.log | \
    /usr/local/bin/ffmpeg -hide_banner -v warning -fflags +genpts -r 25 \
      -f h264 -i pipe:0 -c:v copy -muxdelay 0 -muxpreload 0 \
      -f rtsp -rtsp_transport tcp rtsp://127.0.0.1:5544/gpu_test \
      >>/tmp/opencode/pub.log 2>&1
  rc=$?
  echo "[supervisor] pipeline exited rc=$rc, restart in 2s $(date '+%F %T')" >> /tmp/opencode/supervisor.log
  sleep 2
done
```
