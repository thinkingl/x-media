# RTSP 拉流问题排查记录（VLC 黑屏 / 花屏 / ffmpeg h264 none）

> 本文档记录 2026-08 排查的 RTSP 输出端一系列问题、根因与修复，供后续维护参考。
> 相关代码：`server/internal/media/mp4_source.go`、`rtsp_sink.go`、`rtsp_server.go`。

## 问题现象

用 VLC / ffmpeg 拉 RTSP 流时出现三种现象（随时间演进暴露）：

1. **VLC 黑屏**：连接建立但长时间无画面。
2. **ffmpeg `h264, none: unspecified size`**：无法从 SDP 初始化解码器，`frame=0`。
3. **花屏 / 图像回退**：连接后画面花屏，ffmpeg 报大量
   `corrupted macroblock` / `error while decoding MB` / `non monotonically increasing dts`。

## 根因分析

三个现象其实是**两个独立 bug** 的连锁反应：

### Bug 1：MP4Source 循环回绕永不触发（导致黑屏 + h264 none）

`readLoop` 判断某个 track 是否读完的逻辑错误：

```go
// 修复前（错误）：
f, done, err := m.nextSample(tr)
if err != nil {                 // ← 错：读完时 err=nil
    if done { baseDone = true }
}
```

`nextSample` 读到文件尾时返回 `(nil, true, nil)`——`err` 是 `nil`，
导致 `done` 信号被忽略、`baseDone` 永不置位、**loop 回绕永不触发**。

结果：文件播放到末尾（约 57s）后流**卡死，不再产帧**。
此后新 RTSP reader 连上的是一个"停滞流"：
- VLC 收不到帧 → 黑屏
- ffmpeg 探测时拿不到后续数据 / SPS → `h264 none`

修复：先判断 `done`，再判断 `err`：

```go
f, done, err := m.nextSample(tr)
if done {                        // ← 先判断 done
    if tr == base { baseDone = true }
    continue
}
if err != nil { ... }
```

### Bug 2：GOP 重放污染实时流（导致花屏）

为让新 reader 立即出画面，最初实现"重放最近完整 GOP（含 IDR 及后续 P 帧）"，
通过 `rtph264.Encoder` 把缓存的历史帧重新编码后广播。

**问题**：
- 重放的历史 GOP 数据帧与实时流帧交错，ffmpeg/VLC 解码时参考帧错位 → **花屏**。
- 重放时把 GOP 整体对齐到当前 PTS，`curPTS + offset` 在 loop 回绕瞬间可能为负，
  `uint32` 截断后产生巨大乱序时间戳 → **dts 非单调**。

修复：**不重放数据帧**，改为**周期性重发 SPS/PPS 参数集**：

```go
// 只重发 SPS/PPS（极小），不重放 IDR/P 帧。
if needReplay && len(sps) > 0 && len(pps) > 0 {
    pkts, _ := enc.Encode([][]byte{sps, pps})
    // ...WritePacketRTP
}
```

SPS/PPS 参数集 RTP 包极小，不污染解码流：
- reader 收到后能初始化解码器（解决 ffmpeg 探测失败）
- 完整 GOP 由正常流的下一个自然关键帧提供（GOP 间隔约 2.5s），解码正确、无花屏

### Bug 3：周期重发 SPS/PPS 作为独立帧插入，导致间歇花屏

在 Bug 2 修复时引入了**周期性（每 2s）重发 SPS/PPS 参数集**。经码流二进制对比发现：

- 参考流：SPS/PPS 只在 GOP 边界（每 75 帧一次，共 17 次），且通常伴随 IDR/切片。
- 修复前 RTSP 流：SPS/PPS 出现 46 次，**大量无 IDR 的独立参数集帧**（约每 2s 一个）。

**根因**：周期参数集被 `rtph264.Encoder` 编码成独立 RTP AU 插入实时流。
解码器收到独立 SPS/PPS 会重新初始化，但**无后续 IDR**，接下来若干 P 帧参考失效 →
**间歇花屏/黑帧**，直到下个自然 IDR（≤2.5s）才恢复。表现为"花屏少但没彻底消失"。

修复：**删除周期重发**，SPS/PPS 只在**新 reader PLAY 时发一次**（`sendParamsToStream`）。
数据面保持纯净：SPS/PPS 只在 GOP 边界出现，与参考流一致。

```go
// 修复后：writeVideo 不再插入任何参数集，只发数据帧。
// 参数集仅在新 reader PLAY 时由 sendParamsToStream 发一次。
```

## 验证结果

| 场景 | 修复前 | 修复后 |
|---|---|---|
| SPS/PPS 帧数（60s 流） | 46（大量独立插入） | 18（仅 GOP 边界，同参考 17+1） |
| 帧连续性 | - | 0 跳变，1 次正常回绕 |
| corrupt / 宏块错误 | 间歇出现 | 0 |
| ffmpeg 解码帧数 | - | frame=152 正常 |
| 解码画面（PSNR 对参考） | - | 37.18 dB（视觉无损） |
| loop=true 循环回绕 | 57s 后卡死 | 持续回绕产帧 ✅ |
| ffmpeg TCP 拉流 | `h264 none` / 花屏 | 正常解码 ✅ |
| ffmpeg UDP | 461 Unsupported | 正常解码 ✅ |
| ffmpeg UDP-multicast | 461 Unsupported | 正常解码 ✅ |
| ffmpeg 自动协商 | 461 / none | 正常解码 ✅ |
| 连续多次连接 | 花屏 | 0 corrupt ✅ |
| 12s 长拉 | 花屏/dts乱 | 0 corrupt，frame=193 ✅ |
| 全量测试 + race | - | 通过 |

## 相关单元测试

`server/internal/media/mp4_source_test.go` 新增回归测试：

- `TestMP4Source_LoopWrapsBeyondFile`：加速节流驱动 readLoop 跑完整个文件，
  验证总帧数超过单文件帧数（回绕发生，防止 done bug 复发）。
- `TestMP4Source_LoopPTSMonotonic`：跨回绕点视频 PTS 严格单调递增（防 loopDur 漂移）。
- `TestMP4Source_LoopSingleFileDoesNotLoop`：`loop=false` 读完即停（正确不回绕）。

测试用 `throttleOverride`（1ms）加速 readLoop，避免真实 57s 等待。

`server/internal/media/rtsp_sink_test.go` 新增回归测试：

- `TestRTSPSink_NoGOPDataReplay`：写多个 P 帧，验证无周期参数集/GOP 重放插入
  （RTP 包数 = 帧数，无额外包）。
- `TestRTSPSink_NewReaderParams`：新 reader PLAY 时 `sendParamsToStream` 只发 SPS/PPS
  （≤2 包），不重放数据帧。
- `TestRTSPSink_ConfigureHevc` / `TestSplitCodecConfigHevc`：RTSPSink 支持 HEVC 配置，
  参数集（VPS/SPS/PPS）正确分离。

## source 切换问题（2026-08-13）

**现象**：同一 RTSP sink，在管道上切换到另一个 source（H.265 文件）后拉流黑屏。

**根因**：
1. sink 的 SDP/编码器只在 pipe.Start 时 Configure 一次，且旧 sink 实例复用，
   无 source 切换感知；切到 H.265 后，`splitCodecConfigVideo`（H.264 专用）
   找不到 SPS/PPS → Configure 失败。
2. 当时 Configure 失败被 `Warnf` 静默吞掉，pipe 照常启动，sink 保留旧 H.264 状态，
   把 H.265 帧当 H.264 处理 → 黑屏。

**修复**：
1. `RTSPSink` 支持 H.264 与 H.265 自动协商（`configureLocked` 按 CodecID 分支，
   H.265 用 `format.H265` + `rtph265.Encoder` + VPS/SPS/PPS）。
2. `DefaultPipe.Start`：Configure 失败时回滚 started 并返回错误；
   `PipeService.Start` 失败时返回错误，前端可感知（不再静默黑屏）。

**验证**：同一 sink 在 H.264(1920x1080) 与 H.265(1280x720) 之间双向切换，均正常解码。

## 经验教训

1. **不要甩锅给播放器/ffmpeg**。`h264 none`、花屏几乎总是服务端数据流问题。
2. **循环播放必须回绕**，done 判断要独立于 err（返回 `(nil, true, nil)` 是合法契约）。
3. **RTSP 参数集与数据帧分离**：新 reader 只需 SPS/PPS 初始化，
   数据由实时流的自然关键帧承载；重放历史 GOP 会破坏解码参考链。
4. **时间戳必须严格单调**：任何"对齐/回拨"逻辑在 loop 场景都可能产生负值。
5. **参数集重发必须是"reader 建立时一次"，不可周期插入数据面**：
   独立 SPS/PPS 帧会让解码器重新初始化但无 IDR，导致间歇花屏。
   排查手段：拉流保存后与源文件逐帧对比（帧哈希 + 连续性），
   检查 SPS/PPS 出现频率是否与源一致。
