# x-media GPU Test Source（4K CUDA Fireworks）

持续生成 **3840×2160@25 / H.264 High / 20 Mbps / 无 B 帧** 的测试码流，带毫秒时钟和帧号，供 x-media 管道、RTSP 预览和 GB28181 虚拟摄像头联调使用。

当前部署：**286（x-test）已接入 4K CUDA Fireworks，KDM 预览确认正常**。

---

## 1. 链路

```
CUDA Fireworks（粒子模拟 + splatting + bloom + NV12）
    │  PyNvVideoCodec / NVENC，3840×2160@25，20 Mbps，GOP 25，bf=0
    ▼
Annex-B H.264（stdout）
    │  ffmpeg：h264_cuvid 解码 + h264_nvenc 重编码 + AAC 音频
    ▼
x-media RTSP server  rtsp://127.0.0.1:5544/gpu_test
    │
    ├── x-media input 9a1f… → pipe b9cd… → RTSP 输出 78ac…（本地预览）
    └── x-media input 9a1f… → pipe f5a2… → GB28181 输出 8521…（KDM 286 x-test）
```

- 4K CUDA 渲染器每帧约 **4 ms**；当前带音频的 4K25 管线实测：生成器 CPU ~13%、ffmpeg CPU ~19%（合计约 32% 单核），GPU SM ~17%、NVENC ~34%、NVDEC ~12%。
- **为什么 ffmpeg 要重编码视频**：裸 H.264 管道包没有可靠 PTS，`-c:v copy` 时音频无法正确交织（VLC 会显示音频 codec 但音频帧为 0）。用 `h264_cuvid` 解码 + `h264_nvenc` 重编码后，视频帧有了真实 PTS，AAC 才能正常与视频交织。
- 视频 + AAC 背景音乐（`AUDIO_FILE` 可选）；左上角保留 `YYYY-MM-DD HH:MM:SS.mmm` + `Fxxxxxx` 帧号。
- 4K 大关键帧依赖 x-media RTSP input 的 PTS 修复（commit `08fe39d`）。

---

## 2. 文件

| 文件 | 作用 |
|---|---|
| `fireworks_cuda.py` | 4K CUDA 烟花渲染器：粒子/爆裂/辉光/NV12；PyTorch CUDA extension |
| `gpu_live_4k_fireworks.py` | 4K25 实时生成器：渲染 + 时钟叠加 + NVENC + Annex-B 输出 |
| `supervisor.sh` | 循环拉起生成器 + ffmpeg RTSP 推流；崩溃自动重启 |
| `gpu_live.py` | 1080p mixed 随机块/噪声生成器（备用/对照） |
| `cuda_patterns.py` | 4K CUDA Aurora / Kaleido / Julia 图案原型与性能测试 |
| `prepare_audio.sh` | FLAC → AAC-LC 48k stereo 128k ADTS（音频文件不提交） |
| `install.sh` | 安装 PyNvVideoCodec / ninja（user site） |
| `systemd/x-media-gpu-test.service` | systemd user service 示例 |

相关文档：`../../docs/gpu-test-source.md`（1080p 基线与历史设计）。

---

## 3. 依赖与安装

### 3.1 必要条件

- NVIDIA GPU：Pascal+，本机 GTX 1060 6GB，驱动 535.309.01（NVENC 12.1）
- CUDA 版 PyTorch（本机 `2.9.1+cu126`）
- `PyNvVideoCodec==2.2.3`
- `ninja`（首次编译 CUDA extension 需要）
- ffmpeg（推流只用 `-c:v copy`，不需要 drawtext）
- x-media 已启动，RTSP server 默认 `:5544`

### 3.2 安装

```bash
cd tools/gpu-test-source
./install.sh
```

等价于：

```bash
python3 -m pip install --user --no-deps PyNvVideoCodec==2.2.3
python3 -m pip install --user ninja
```

首次运行会编译 CUDA extension（`~/.cache/torch_extensions`），通常 1–2 分钟；之后加载缓存。

### 3.3 验证

```bash
python3 - <<'PY'
import torch, PyNvVideoCodec
print(torch.__version__, torch.cuda.is_available())
print(PyNvVideoCodec.__version__)
PY
```

---

## 4. 运行

### 4.1 supervisor（推荐）

```bash
cd tools/gpu-test-source
RTSP_URL=rtsp://127.0.0.1:5544/gpu_test \
LOG_DIR=/tmp/x-media-gpu-test \
./supervisor.sh
```

日志：

- `$LOG_DIR/gen.log`：生成器 5s 统计（fps / Mbps / CPU）
- `$LOG_DIR/pub.log`：ffmpeg 推流
- `$LOG_DIR/supervisor.log`：启动/退出/重启

### 4.2 手工运行（调试）

```bash
# 写 5 秒裸 H.264 文件
python3 gpu_live_4k_fireworks.py --duration 5 --out /tmp/fw4k.h264

# 封装 MP4
ffmpeg -y -f h264 -framerate 25 -i /tmp/fw4k.h264 -c copy -movflags +faststart /tmp/fw4k.mp4

# 实时推 RTSP（带音频；视频用 cuvid 解码 + NVENC 重编码）
python3 gpu_live_4k_fireworks.py --mbps 20 --duration 0 | \
  /usr/local/bin/ffmpeg -hide_banner -v warning -fflags +genpts -r 25 \
    -f h264 -c:v h264_cuvid -i pipe:0 \
    -stream_loop -1 -re -i /tmp/opencode/fireworks_audio.aac \
    -map 0:v:0 -map 1:a:0 -c:a aac -b:a 128k -ar 48000 -ac 2 \
    -c:v h264_nvenc -preset p4 -rc cbr -b:v 20M -maxrate 20M -bufsize 20M -g 25 -bf 0 \
    -muxdelay 0 -muxpreload 0 -avoid_negative_ts make_zero \
    -f rtsp -rtsp_transport tcp rtsp://127.0.0.1:5544/gpu_test
```

### 4.3 关键参数

```bash
python3 gpu_live_4k_fireworks.py --help
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `--width/--height` | 3840/2160 | CUDA renderer 固定 4K |
| `--fps` | 25 | |
| `--mbps` | 20 | CBR 目标码率 |
| `--duration` | 0 | 0 = 无限 |
| `--out` | `-` | stdout 或 `.h264` |
| `--bursts` | 32 | 同时管理的烟花组数 |
| `--particles` | 256 | 每组最大粒子数 |

---

## 5. 音频

测试源支持把一首歌循环作为 AAC 背景音乐。音频素材不提交到仓库；只在 `/tmp` 转换和运行。

### 5.1 转换

```bash
cd tools/gpu-test-source
./prepare_audio.sh "/tmp/opencode/黄霄雲 - 烟火里的尘埃.flac"                     /tmp/opencode/fireworks_audio.aac
```

默认输出：**AAC-LC / 48 kHz / stereo / 128 kbps / ADTS**。

### 5.2 supervisor 自动加载

`supervisor.sh` 默认读取：

```bash
AUDIO_FILE=/tmp/opencode/fireworks_audio.aac
```

文件存在时，ffmpeg 推流会自动加：

```bash
-stream_loop -1 -re -i "$AUDIO_FILE"
-map 0:v:0 -map 1:a:0 -c:a aac -b:a 128k -ar 48000 -ac 2
-avoid_negative_ts make_zero
```

视频侧必须使用 `h264_cuvid` 解码 + `h264_nvenc` 重编码（supervisor 已包含），否则裸 H.264 的 PTS 不可靠，音频不会产生 RTP 包。

- `-stream_loop -1`：无限循环整首歌。
- `-re`：音频按实时速度读取，与视频节流配合。
- 音频重新编码为 AAC（ADTS 直接 copy 到 RTSP 会报 `AAC with no global headers`）。
- `-avoid_negative_ts make_zero`：避免 RTSP 起始时间为负。
- 测试实测音视频起始 PTS 偏差约 **50–150 ms**，对背景音乐足够。
- 4K25 双编码实测：生成器 CPU ~13%、ffmpeg CPU ~19%，GPU SM ~17%、NVENC ~34%、NVDEC ~12%。

没有音频文件时，supervisor 自动退回视频-only。

### 5.3 286 GB28181 音频

286 输出需要 `audio_enable: true`，否则 GB28181 sink 不会封装 AAC。当前 286 配置已启用；切换/回退管道后如果配置被重置，请重新确认。

---

## 6. x-media 接入

### 5.1 创建 input/output/pipe

```bash
API=http://127.0.0.1:18090/api/v1
KEY=dev-api-key

# input: 拉 gpu_test
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"name":"GPU Test Source","type":"rtsp","config":"{\"url\":\"rtsp://127.0.0.1:5544/gpu_test\",\"transport\":\"tcp\",\"reconnect\":true,\"reconnect_interval_ms\":1000}"}' \
  $API/inputs

# 本地 RTSP 输出
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"name":"GPU Test RTSP Out","type":"rtsp","config":"{\"mode\":\"server\",\"addr\":\":5544\"}"}' \
  $API/outputs

# pipe
curl -s -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"input_id":"<INPUT_ID>","output_id":"<OUTPUT_ID>"}' \
  $API/pipes

# 启动顺序：output → input → pipe
curl -s -X POST -H "X-API-Key: $KEY" $API/outputs/<OUTPUT_ID>/start
curl -s -X POST -H "X-API-Key: $KEY" $API/inputs/<INPUT_ID>/start
curl -s -X POST -H "X-API-Key: $KEY" $API/pipes/<PIPE_ID>/start
```

当前对象：

| 对象 | ID |
|---|---|
| input | `9a1f1a00-6476-6250-60e4-bb3fba5482dd`（GPU Test Source） |
| RTSP output | `78ac5050-a745-b6f6-a83b-5f5f3b88e6ce`（GPU Test RTSP Out） |
| RTSP pipe | `b9cd2cbb-9045-dec3-34c8-faa84558ea95` |
| GB28181 output（286） | `85211b63-272c-0c18-50b8-4840a51209d6` |
| 286 pipe | `f5a25350-0884-d1f1-563a-db4a6d4bbdb2` |

### 5.2 286（x-test）GB28181

- 设备：`32000000001120000286`
- 通道：`32000000001320003627`
- 管道：`9a1f… → 8521…`
- 切换/回退见 `../../docs/gpu-test-source.md` §6.4
- 平台预览为手动触发；4K 通过 GB28181/WebRTC 时，平台侧可能转码，首帧/带宽以平台为准。

### 5.3 本地预览

```bash
# 4K 源
ffplay -hwaccel cuda -fs rtsp://192.168.88.102:5544/gpu_test

# x-media 4K RTSP 输出
ffplay -hwaccel cuda -fs rtsp://192.168.88.102:5544/live/78ac5050-a745-b6f6-a83b-5f5f3b88e6ce
```

> 4K H.264 软件解码在 4 核机器上可能跑不到 1x；优先用 `-hwaccel cuda`/VLC 硬解。

---

## 7. 渲染器说明

`fireworks_cuda.py` 在 CUDA 中完成：

1. **Burst 状态**：位置、颜色、出生时间、寿命、深度 `z`、衰减、风向、粒子数。
2. **粒子**：方向、速度、亮度、闪烁相位/频率、寿命系数；每帧按 age 计算径向扩散 + 重力。
3. **距离/大小随机**：`z=0.25~1.0`；近处更大/更亮/寿命更长，远处更小/更暗/偏高。
4. **HDR 核心 + 1/4 分辨率 bloom**：5×5 splat + 低分辨率 glow + 3×3 blur + 上采样，最后 tone mapping。
5. **NV12 直出**：BT.709 limited range，Y/UV 在 CUDA 中写好后直接送 NVENC。

默认 32 组 × 256 粒子；寿命约 2.2–6.5 s，带随机衰减和错峰重生。

---

## 8. x-media PTS 修复

4K 大关键帧会被 RTP 拆成大量 FU-A 分片。旧版 RTSP input 在 AU 完成包上才调用 `PacketPTS`，导致 `timeDecoder` 永不初始化，所有帧 PTS=0，RTSP/GB 输出时间戳错乱。

修复（commit `08fe39d`）：

- 在 `dec.Decode()` 前调用 `PacketPTS`，让 IDR 起始分片完成初始化；
- 未初始化的帧丢弃，避免 PTS=0 传给下游；
- 回归测试：`server/internal/media/rtsp_input_pts_test.go::TestRTSPInput_PTSForFragmentedIDR`；
- `go test ./...` 全绿；当前运行二进制已包含该修复。

---

## 9. 故障排查

| 现象 | 处理 |
|---|---|
| `ModuleNotFoundError: PyNvVideoCodec` | `./install.sh` 或 `pip install --user PyNvVideoCodec==2.2.3` |
| CUDA extension 编译失败 | 确认 `ninja`、`nvcc`、`TORCH_CUDA_ARCH_LIST`；首次编译需 1–2 分钟 |
| `Preset p4 not found` | PyNvVideoCodec preset 必须大写：`P4` |
| x-media input 404 | publisher 未起或路径未注册；先启动生成器，等 `RTSP publisher connected: gpu_test` |
| x-media 重启后 input/pipe 停止 | 通过 API/UI 重新 start；publisher 会自动重连 |
| 4K 本地 TCP 预览卡顿/丢包 | 用硬件解码；4K RTSP sink 对慢 reader 会背压；286 走 UDP 不受此影响 |
| `Timestamps are unset in a packet` | 裸 H.264 首包警告；RTP 时间戳仍为 40ms 递增，可忽略 |
| KDM 286 无画面 | 查 286 pipe `dropped=0`、设备注册/心跳、平台重新预览；4K 平台兼容性/带宽另查 |
| 需要音频/无声音 | 确认 `AUDIO_FILE` 存在、supervisor 日志 `audio: ...`、286 输出 `audio_enable:true`；AAC 必须重新编码不能直接 ADTS copy 到 RTSP |

---

## 10. systemd（可选）

```bash
mkdir -p ~/.config/systemd/user
cp systemd/x-media-gpu-test.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now x-media-gpu-test.service
loginctl enable-linger "$USER"   # 需要权限；否则登录后才启动
```

默认 unit 假设仓库在 `%h/code/x-media`，日志在 `%h/.local/state/x-media-gpu-test`；如路径不同请修改 `ExecStart`。

---

## 11. 安全与注意

- 文档中的 `dev-api-key` 仅用于本机开发配置；生产环境不要提交/暴露 API key。
- 测试源会占用一个 NVENC session；与其它 4K 编码任务并行时注意 GPU 资源。
- 4K25 20 Mbps 对 GB28181 平台的网络/转码能力有要求；若平台侧不稳定，可先降 `--mbps` 或改 1080p。
- 生成的视频/截图不要提交到仓库（本目录只提交代码/脚本/文档）。
