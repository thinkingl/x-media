# RTSP AAC（MPEG4-GENERIC）AU Header 布局问题：前因后果与解决方案

> 相关修复：`c5614d1`（引入问题）→ `bb396a7`（修正）
> 涉及代码：`server/internal/media/rtsp_sink.go`（`mpeg4GenericAUHeader`）、
> `server/internal/media/rtsp_input.go`（`parseMPEG4GenericAUs`）

## 1. 问题现象

RTSP 输出（`rtsp://host:5544/live/<output_id>`）的音频流：

- SDP 正常通告音频：`m=audio ... MPEG4-GENERIC/44100/2`，`fmtp: config=1210; sizelength=13; indexlength=3; mode=AAC-hbr`；
- 服务端日志确认音频帧持续写入（`RTSP sink WriteFrame ... type=1`）；
- **但 ffmpeg 拉流音频零采样**，持续报错：

```
[rtsp] First AU larger than packet size
    Last message repeated 220 times
```

`ffprobe` 能列出音频流（SDP 解析正常），`ffmpeg -vn -af volumedetect` 却
`n_samples: 0`——即 RTP 层有包、解包层全部丢弃。

## 2. 排查过程

### 2.1 抓包验证发送字节

用最小 RTSP 客户端（TCP interleaved）直接读服务端发出的音频 RTP 负载：

```
payload len= 361 first12: 01 00 b2 80 21 19 c5 44 ...  AU_len_bits=16 parsed_AU_size=357
payload len= 401 first12: 01 00 c6 80 21 1a 05 05 ...  AU_len_bits=16 parsed_AU_size=397
...
```

按 **RFC 3640 位打包**（AU-header 紧跟 12bit 长度域，从 bit12 起）解析：
所有包头正确（`size + 4 == payload len`）。看起来"完全合规"。

### 2.2 分叉点：AU size ≥ 512

抓到的样本 AU 尺寸全部 < 512。此时两种候选布局**恰好产生相同字节**
（因为 size 的高 4 bit `size[12:9] == 0`，位打包布局的 bit12–15 半字节为
`0000`，与字节对齐布局的填充一致）。一旦 AU ≥ 512，两种布局分叉：

| AU size | RFC 12bit 紧打包（当时实现） | 字节对齐（ffmpeg 期望） |
|---|---|---|
| 357 | `01 00 b2 80` | `01 00 b2 80`（相同） |
| 600 | `01 01 2c 00` | `00 10 12 c0`（完全不同） |

歌曲类 AAC 的 AU 尺寸分布可达数百 bytes，峰值必然越过 512。

### 2.3 金标准：ffmpeg 源码

`libavformat/rtpdec_mpeg4.c` 的 `rtp_parse_mp4_au()`（FFmpeg 7.0）：

```c
/* decode the first 2 bytes where the AUHeader sections are stored
   length in bits */
au_headers_length = AV_RB16(buf);                       // 按 2 字节读长度域
data->au_headers_length_bytes = (au_headers_length + 7) / 8;

/* skip AU headers length section (2 bytes) */
buf += 2;                                               // AU-header 从字节 2 开始
len -= 2;

init_get_bits(&getbitcontext, buf, ...);                // 从 bit16 起位打包
...
data->au_headers[i].size = get_bits_long(&gb, size_length);
```

即 ffmpeg 的布局是：**2 字节 AU-headers-length + AU-header 字节对齐**
（长度域占满 16bit，AU-header 从下一个字节开始位打包）。

用 12bit 紧打包的字节 `01 00 b2 80` 喂给它：从 bit16 读 13bit 得
`1011001010000` = **5712** → `5712*8 > 剩余负载` →
"First AU larger than packet size"。与实测报错完全吻合。

## 3. 前因：问题是怎么引入的

时间线（详见对应 commit）：

1. **原始实现**（`32ae5c4` 引入）：sink 用的正是**字节对齐布局**
   （`00 10` + `size<<3`）——与 ffmpeg 兼容。当时的真正缺陷是另一件事：
   **RTSP 输入侧完全没有音频解包器**（`format.MPEG4Audio` 分支缺失、音频
   RTP 回调从未注册），自家拉流收不到任何音频帧。
2. **`c5614d1`（补齐 RTSP 音频）**：正确地补上了输入侧解包器，但同时
   把 sink 的帧格式"修正"为 RFC 3640 的 12bit 紧打包——依据是对 RFC
   §3.2.1 的字面解读（"AU-headers-length 是 12 bit"）。改动后自家
   sink→input 回环测试全绿，于是被判定为"修复了 VLC 兼容性"。
3. **实际后果**：自家 sink 与 input 共享同一个错误布局假设（同错相消），
   回环测试无法暴露问题；而 ffmpeg/VLC 等第三方客户端全部无法解码音频。
   直到对真实 URL 做 ffmpeg 实测才暴露。

## 4. 解决方案（`bb396a7`）

**原则：互操作以主流实现为准，格式类修复必须用第三方客户端做金标准验证。**

### 4.1 输出侧（`rtsp_sink.go`）

`mpeg4GenericAUHeader` 改回字节对齐布局，并注明兼容依据：

```go
func mpeg4GenericAUHeader(auSize int) []byte {
	h := uint16(auSize) << 3 // Size(13bit) | Index(3bit=0)
	return []byte{0x00, 0x10, byte(h >> 8), byte(h)}
}
```

### 4.2 输入侧（`rtsp_input.go`）

解包抽为纯函数 `parseMPEG4GenericAUs`，与 ffmpeg 同布局解析
（`AV_RB16` 语义：长度域按 2 字节读、AU-header 从 bit16 起位打包），
支持一包多 AU，畸形负载（长度域越界/截断/AU 尺寸越界）整体拒绝：

```go
auHeadersBits := int(p[0])<<8 | int(p[1]) // ffmpeg: AV_RB16
headerBytes = 2 + (auHeadersBits+7)/8
bitOffset := 16 // 长度域占 2 字节，AU-header 从 bit16 起
```

### 4.3 验证

| 层级 | 测试 | 结果 |
|---|---|---|
| 字节断言 | `TestMpeg4GenericAUHeaderByteAligned`（含 357 分叉尺寸） | 绿 |
| 解析器 | `TestParseMPEG4GenericAUsByteAligned`（单 AU / 多 AU / 截断） | 绿 |
| 回环 | `TestRTSPInputReceivesAACAudio`（sink→input） | 绿 |
| 全量 | `go test ./...`（含 200s+ 媒体测试） | 绿 |
| **实测** | `ffmpeg -rtsp_transport tcp -i rtsp://... -t 6 -vn -af volumedetect` | `mean_volume: -18.8 dB`、`max_volume: -1.4 dB`（真实音乐电平），无 AU 错误 |

实测命令（排障可直接复用）：

```bash
# 1. 确认 SDP 音频通告
ffprobe -hide_banner -rtsp_transport tcp 'rtsp://<host>:5544/live/<output_id>'

# 2. 确认音频实际解码且非静音（静音约 -91dB）
ffmpeg -hide_banner -rtsp_transport tcp -i 'rtsp://<host>:5544/live/<output_id>' \
       -t 6 -vn -af volumedetect -f null -
```

## 5. 经验教训

1. **RFC 字面 ≠ 生态事实**：RFC 3640 §3.2.1 的图示是 12bit 紧打包，
   但 ffmpeg 等主流实现按"2 字节长度域 + 字节对齐"解析。流媒体协议
   互操作应以主流实现为金标准，并在 SDP fmtp 语境下实测验证。
2. **双端自测的盲区**：本项目同时实现 sink 与 input，两端共享同一错误
   假设时回环测试全绿（同错相消）。凡涉及"对外格式"的改动，必须引入
   第三方实现（ffmpeg/VLC）做交叉验证，回环测试只能证明自洽。
3. **小样本误判**：抓包只看前 8 个包（恰好全部 < 512）差点得出"字节
   正确"的结论；分叉条件（size ≥ 512）要求样本覆盖边界值。
4. **改"兼容性修复"前先查现网行为**：原始字节对齐实现本就是 ffmpeg
   兼容的，"按 RFC 修正"反而引入回归。判断"现有实现是否真不兼容"
   需要客户端实测，而不是只读规范。
