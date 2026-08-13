# X-Media 测试方案（Media Pipe Mock 三角验证）

> 本方案取代旧测试方案，是当前唯一权威测试策略。架构与标准见 `design.md` 第 12 节。

## 1. 核心思想

管道标准使每个组件只依赖契约、不依赖对端实现，因此每个组件完工后可用 **一个真实组件 + 两个 mock** 独立验证。任何改动都被约束在 mock 边界内，回归成本最低。

```
        MockPipe(内存通道)
           ↕ 标准帧/信令
  真实 Source ───────────► MockSink(记录/断言)
        MockPipe
           ↕ 标准帧/信令
  真实 Pipe ◄────────────► MockSource(可编程产帧) + MockSink(记录/断言)
        MockPipe
           ↕ 标准帧/信令
  真实 Sink ◄────────────► MockSource(可编程产帧)
```

## 2. Mock 三件套

- **MockSource**：按测试剧本可编程产帧（合法 H.264 AU、AAC 帧），响应信令（`Subscribe`→回预设 `StreamInfo`、处理 `Seek`/`Pause`），记录收到的控制请求。
- **MockSink**：接收帧并断言（顺序/字节/时间戳/关键帧），可发起信令请求。
- **MockPipe**：内存双向通道——真实 source 可推帧、真实 sink 可消费，信令 request/response + 事件两路齐全。

## 3. 四层测试金字塔

### 3.1 L1 契约层（纯单元，无 mock）

- FrameHeader 编解码 round-trip、30 字节边界、magic/version 校验、截断/损坏
- 信令 Envelope 编解码 round-trip
- ClockRate 换算表驱动（→ms、→90kHz，视频/音频不同 timescale）

### 3.2 L2 组件层（真实 + 两 mock）——核心

| 被测组件 | MockSource | MockPipe | MockSink | 关键断言 |
|---|---|---|---|---|
| MP4Source | ✓ | ✓ | | Subscribe 返回正确 SPS/PPS/AAC config/ClockRate；首帧=keyframe；每 AU 一个 sample；PTS/DTS 与 mp4ff 一致且单调；音视频交错；Loop/Seek/Pause/Stop 语义 |
| RTSPInput | ✓ | ✓ | | 拉流→depacketize→标准帧内容正确；动态 InfoUpdate（音轨后到） |
| Pipe | ✓ | ✓ | | 信令路由、帧转发（顺序/字节一致）、fan-out 双 sink 独立全量、背压丢帧计数且不阻塞 source、Unsubscribe 停收、事件下发 |
| RTSP server sink | ✓ | ✓ | | StreamInfo→正确 SDP/format；rtph264 封装字节级校验；ClockRate 换算 |
| HTTP-FLV sink | ✓ | ✓ | | flv header + sequence header（avcC/ASC）+ tag 头（type 8/9、时间戳 ms、CTS）字节级校验 |
| RTMP sink | ✓ | ✓ | | 握手 C0/C1/C2、connect AMF0、sequence header、tag 时间戳、24 位回绕、断线重连 |

### 3.3 L3 半集成（两个真实 + 一个 mock）

- 真实 MP4Source + 真实 Pipe + MockSink：标准帧在真实链路无损
- MockSource + 真实 Pipe + 真实 RTMP sink：标准→协议字节
- 真实 MP4Source + 真实 Pipe + 真实 HTTP-FLV sink：离线端到端（flv 落文件/内存校验）

### 3.4 L4 全真端到端（需外部依赖，手动/CI 可选）

- file→rtmp/rtsp/http-flv 用 ffprobe/ffplay 实测（扩展 `server/test/integration/api_test.sh`）
- **基准参照式 E2E（内容锚定，推荐）**：`server/test/e2e/`（`make e2e`，需 ffmpeg）。
  手工生成的基准 MP4（帧号条码 + 7 段数码 + DTMF 音频）经真实 MP4Source→Pipe→RTSPSink
  推送，ffmpeg 拉流解码后与基准逐帧比对，定量校验丢帧/重复/损坏/内容不一致。
  详见 `docs/e2e-reference-test.md`。

## 4. 测试数据策略

- **合成字节 fixture**（确定性、离线、毫秒级）：合法 SPS/PPS/IDR 字节 + AAC 帧 + AudioSpecificConfig，用于协议封装测试。
- **真实文件 fixture**（`server/test/fixtures/test.mp4`，已在仓库）：用于 MP4Source 的 PTS/DTS/交错/Loop 断言。
- 两种并存：合成字节测协议封装，真实文件测 demux 正确性。

## 5. RTMP mock server

原生 RTMP sink 测试需要最小 RTMP server（握手 C0/C1/C2 + chunk 解析 + AMF0 假服务端）接收字节并断言，约 200 行，属于测试基建的一部分。确保 RTMP sink 可离线完整验证。

## 6. 时序处理约定

- 流式异步一律用 channel + 超时等待（`require.Eventually` / 带 deadline 的 select），**禁用 `time.Sleep` 猜测时序**，减少 flaky。
- 并发相关的断言放在接收 goroutine 内完成后再退出，避免竞态。

## 7. 与现有测试的关系

现有 `MockInputStream`/`MockOutputStream`/`MockMediaEngine`（绑定旧接口）随新架构替换为 `MockSource`/`MockSink`/`MockPipe`。服务层测试（CRUD/校验）保留 mock repo 模式不变。

## 8. 实施顺序

按 **L1 契约层 → L2 组件层（MP4Source → Pipe → RTSP server sink → HTTP-FLV sink → RTMP sink → RTSPInput）→ L3 半集成 → L4 端到端** 顺序，每完成一个组件即用 mock 三角验证，再进入下一个。

### 8.1 实施状态【已完成】

- **L1 契约层**：`frame_test.go` / `signal_test.go` / `clock_test.go` 全通过
- **L2 组件层**：MP4Source / DefaultPipe / RTSPSink / HTTPFLVSink / RTMPSink / RTSPInput 均通过 Mock 三角验证（`mock source/sink/pipe`）
- **L3 半集成**：MP4→HTTP-FLV、MP4→RTSP→RTSP 中继通过
- **L4 端到端**：文件→RTSP server / HTTP-FLV 拉流解码通过（见 design.md 12.9.1）
- **循环回绕**：`TestMP4Source_AlignLoopAlignment` 验证统一基准回绕后音视频绝对时间轴一致

### 8.2 RTSP transport 适配【已完成】

ffmpeg 全 transport 实测通过（`l4_transport_test.sh`）：

| transport | 结果 |
|---|---|
| TCP (`-rtsp_transport tcp`) | ✅ frame=100 |
| UDP (`-rtsp_transport udp`) | ✅ frame=108 |
| UDP-multicast | ✅ frame=243（loop 回绕有 dts 警告，非致命） |
| Default（自动协商） | ✅ frame=69 |

配置项（`config.yaml`）：`rtsp_udp_rtp_addr` / `rtsp_udp_rtcp_addr` / `rtsp_multicast_ip` / `rtsp_multicast_rtp_port` / `rtsp_multicast_rtcp_port`。

### 8.3 去除 CGO 依赖【已完成】

- SQLite 驱动从 `gorm.io/driver/sqlite`（依赖 `mattn/go-sqlite3`，CGO）替换为 `github.com/glebarez/sqlite`（纯 Go，基于 `modernc.org/sqlite`）
- `CGO_ENABLED=0` 构建通过，产物为静态链接
- Dockerfile 移除 `gcc`/`libsqlite3-dev`/`libsqlite3-0`

## 9. 端到端测试矩阵（L4，验收基线）

| # | 输入 | 输出 | 验证方式 | 优先级 |
|---|------|------|---------|--------|
| T1 | 文件 H264 | RTSP server | ffprobe 拉流解码 0 错误 | P0 |
| T2 | 文件 H264 | RTMP | ffplay/ffprobe 拉流 | P0 |
| T3 | 文件 H264 | HTTP-FLV | ffplay/curl 拉流 | P0 |
| T4 | 文件 H265 | RTSP server | ffplay/ffprobe 拉流 | P1 |
| T5 | RTSP 拉流 | RTSP server | 转发验证 | P1 |
| T6 | 文件 | 多输出 fan-out | 同时 RTMP + HTTP-FLV | P1 |
| T7 | 文件 | 多管道并发 | 2 个文件→2 个输出 | P2 |
