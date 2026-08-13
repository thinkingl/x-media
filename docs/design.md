# X-Media 媒体流处理平台设计方案

## 1. 项目概述

X-Media 是一个前后端分离的媒体流处理平台，支持多种媒体协议的输入输出，可以在输入输出端之间建立媒体流管道连接，并提供图形化的数据流监控和统计功能。

## 2. 技术选型（历史作废）

> 本节及原第 3 节（旧 LAL 系统架构）已随架构重构作废。旧方案基于 LAL 媒体引擎 + ffmpeg 直读文件，已被第 12 节"媒体管道标准"取代。技术栈沿用 Go + Gin + GORM/SQLite + Vue3。

## 3. 系统架构（历史作废）

> 见第 12 节"媒体管道标准"。架构核心为：Source 适配层 → 标准帧/信令管道 → Sink 适配层。
## 4. 数据库设计

### 4.1 表结构

```sql
-- 输入端配置表
CREATE TABLE inputs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,  -- 'file', 'rtmp', 'rtsp', 'hls', 'http-flv'
    config TEXT NOT NULL,  -- JSON格式的配置
    status TEXT DEFAULT 'stopped',  -- 'stopped', 'running', 'error'
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 输出端配置表
CREATE TABLE outputs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,  -- 'rtmp', 'rtsp', 'hls', 'http-flv', 'file'
    config TEXT NOT NULL,  -- JSON格式的配置
    status TEXT DEFAULT 'stopped',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 管道连接表
CREATE TABLE pipes (
    id TEXT PRIMARY KEY,
    input_id TEXT NOT NULL,
    output_id TEXT NOT NULL,
    status TEXT DEFAULT 'stopped',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (input_id) REFERENCES inputs(id),
    FOREIGN KEY (output_id) REFERENCES outputs(id)
);

-- 日志配置表
CREATE TABLE log_configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT DEFAULT 'info',
    max_size INTEGER DEFAULT 100,  -- MB
    max_backups INTEGER DEFAULT 5,
    max_age INTEGER DEFAULT 30,  -- days
    compress BOOLEAN DEFAULT true,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 统计数据表 (可选，也可以只存内存)
CREATE TABLE stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,  -- 'input', 'output', 'pipe'
    entity_id TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    bytes_in BIGINT DEFAULT 0,
    bytes_out BIGINT DEFAULT 0,
    bitrate BIGINT DEFAULT 0,
    fps REAL DEFAULT 0,
    connections INTEGER DEFAULT 0
);
```

### 4.2 配置参数详细说明

#### 4.2.1 输入端配置

**本地MP4文件输入配置：**

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| path | string | 是 | - | MP4文件路径，支持绝对路径和相对路径 |
| loop | boolean | 否 | true | 是否循环播放 |
| speed | float | 否 | 1.0 | 播放速度，1.0为原速，0.5为半速，2.0为倍速 |
| audio_enable | boolean | 否 | true | 是否包含音频流 |
| video_enable | boolean | 否 | true | 是否包含视频流 |

```json
{
    "path": "/data/videos/test.mp4",
    "loop": true,
    "speed": 1.0,
    "audio_enable": true,
    "video_enable": true
}
```

**RTSP输入配置：**

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| url | string | 是 | - | RTSP流地址，格式：`rtsp://[user:password@]host[:port]/path` |
| transport | string | 否 | "udp" | 传输协议，可选值："tcp"（interleaved模式）、"udp" |
| timeout_ms | integer | 否 | 10000 | 连接超时时间（毫秒） |
| audio_enable | boolean | 否 | true | 是否接收音频流 |
| video_enable | boolean | 否 | true | 是否接收视频流 |
| reconnect | boolean | 否 | true | 断线是否自动重连 |
| reconnect_interval_ms | integer | 否 | 5000 | 重连间隔时间（毫秒） |
| username | string | 否 | - | 认证用户名（当URL中未包含时使用） |
| password | string | 否 | - | 认证密码（当URL中未包含时使用） |

```json
{
    "url": "rtsp://192.168.1.100:554/stream1",
    "transport": "tcp",
    "timeout_ms": 10000,
    "audio_enable": true,
    "video_enable": true,
    "reconnect": true,
    "reconnect_interval_ms": 5000,
    "username": "admin",
    "password": "password123"
}
```

#### 4.2.2 输出端配置

**RTMP输出配置：**

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| url | string | 是 | - | RTMP推流地址，格式：`rtmp://host[:port]/app/stream_key` |
| chunk_size | integer | 否 | 4096 | RTMP chunk大小（字节） |
| reconnect | boolean | 否 | true | 断线是否自动重连 |
| reconnect_interval_ms | integer | 否 | 5000 | 重连间隔时间（毫秒） |
| connect_timeout_ms | integer | 否 | 10000 | 连接超时时间（毫秒） |
| write_timeout_ms | integer | 否 | 5000 | 写数据超时时间（毫秒） |
| tls_enable | boolean | 否 | false | 是否启用RTMPS（TLS加密） |
| tls_cert_file | string | 否 | - | TLS证书文件路径（tls_enable为true时必填） |
| tls_key_file | string | 否 | - | TLS密钥文件路径（tls_enable为true时必填） |

```json
{
    "url": "rtmp://live.example.com/live/stream_key",
    "chunk_size": 4096,
    "reconnect": true,
    "reconnect_interval_ms": 5000,
    "connect_timeout_ms": 10000,
    "write_timeout_ms": 5000,
    "tls_enable": false
}
```

**RTSP输出配置：**

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| mode | string | 是 | - | 输出模式："push"（推流到远程服务器）、"server"（本地启动服务） |
| url | string | 条件必填 | - | 推流目标地址（mode为"push"时必填），格式：`rtsp://[user:password@]host[:port]/path` |
| addr | string | 条件必填 | ":5544" | 本地监听地址（mode为"server"时使用） |
| transport | string | 否 | "tcp" | 传输协议，可选值："tcp"（interleaved模式）、"udp" |
| reconnect | boolean | 否 | true | 断线是否自动重连（mode为"push"时有效） |
| reconnect_interval_ms | integer | 否 | 5000 | 重连间隔时间（毫秒） |
| auth_enable | boolean | 否 | false | 是否启用认证（mode为"server"时有效） |
| username | string | 否 | - | 认证用户名 |
| password | string | 否 | - | 认证密码 |
| tls_enable | boolean | 否 | false | 是否启用RTSPS（TLS加密） |
| tls_cert_file | string | 否 | - | TLS证书文件路径 |
| tls_key_file | string | 否 | - | TLS密钥文件路径 |

```json
{
    "mode": "push",
    "url": "rtsp://media.example.com/live/stream1",
    "transport": "tcp",
    "reconnect": true,
    "reconnect_interval_ms": 5000
}
```

```json
{
    "mode": "server",
    "addr": ":5544",
    "transport": "tcp",
    "auth_enable": true,
    "username": "admin",
    "password": "password123"
}
```

**HTTP-FLV输出配置：**

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| addr | string | 否 | ":8080" | HTTP服务监听地址 |
| url_pattern | string | 否 | "/" | URL路径前缀 |
| enable_https | boolean | 否 | false | 是否启用HTTPS |
| https_addr | string | 否 | ":4433" | HTTPS监听地址 |
| tls_cert_file | string | 否 | - | TLS证书文件路径 |
| tls_key_file | string | 否 | - | TLS密钥文件路径 |
| gop_num | integer | 否 | 0 | GOP缓存数量，0表示不缓存 |
| single_gop_max_frame_num | integer | 否 | 0 | 单个GOP最大帧数，0表示不限制 |
| cross_domain | boolean | 否 | true | 是否允许跨域访问 |
| cors_origin | string | 否 | "*" | 允许的跨域来源 |

```json
{
    "addr": ":8080",
    "url_pattern": "/live/",
    "enable_https": false,
    "gop_num": 2,
    "single_gop_max_frame_num": 0,
    "cross_domain": true,
    "cors_origin": "*"
}
```

### 4.3 管道配置

管道连接输入端和输出端，支持以下配置：

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| input_id | string | 是 | - | 输入端ID |
| output_id | string | 是 | - | 输出端ID |
| auto_start | boolean | 否 | false | 创建后是否自动启动 |
| buffer_size | integer | 否 | 1024 | 数据缓冲区大小（KB） |
| restart_on_error | boolean | 否 | true | 发生错误时是否自动重启 |
| max_restart_count | integer | 否 | 3 | 最大自动重启次数，-1表示无限重试 |
| restart_interval_ms | integer | 否 | 5000 | 重启间隔时间（毫秒） |

```json
{
    "input_id": "input_001",
    "output_id": "output_001",
    "auto_start": true,
    "buffer_size": 1024,
    "restart_on_error": true,
    "max_restart_count": 3,
    "restart_interval_ms": 5000
}
```

## 5. API 设计

### 5.1 RESTful API

#### 输入端管理
```
POST   /api/v1/inputs          创建输入端
GET    /api/v1/inputs          获取所有输入端
GET    /api/v1/inputs/:id      获取单个输入端
PUT    /api/v1/inputs/:id      更新输入端
DELETE /api/v1/inputs/:id      删除输入端
POST   /api/v1/inputs/:id/start  启动输入端
POST   /api/v1/inputs/:id/stop   停止输入端
```

#### 输出端管理
```
POST   /api/v1/outputs         创建输出端
GET    /api/v1/outputs         获取所有输出端
GET    /api/v1/outputs/:id     获取单个输出端
PUT    /api/v1/outputs/:id     更新输出端
DELETE /api/v1/outputs/:id     删除输出端
POST   /api/v1/outputs/:id/start  启动输出端
POST   /api/v1/outputs/:id/stop   停止输出端
```

#### 管道管理
```
POST   /api/v1/pipes           创建管道
GET    /api/v1/pipes           获取所有管道
GET    /api/v1/pipes/:id       获取单个管道
DELETE /api/v1/pipes/:id       删除管道
POST   /api/v1/pipes/:id/start  启动管道
POST   /api/v1/pipes/:id/stop   停止管道
```

#### 统计信息
```
GET    /api/v1/stats           获取总体统计
GET    /api/v1/stats/inputs/:id  获取输入端统计
GET    /api/v1/stats/outputs/:id  获取输出端统计
GET    /api/v1/stats/pipes/:id   获取管道统计
```

#### 日志管理
```
GET    /api/v1/logs            获取日志列表
GET    /api/v1/logs/config     获取日志配置
PUT    /api/v1/logs/config     更新日志配置
GET    /api/v1/logs/stream     WebSocket日志流
```

### 5.2 WebSocket 事件

```json
// 流状态变化
{
    "event": "stream.status",
    "data": {
        "entity_type": "input",
        "entity_id": "xxx",
        "status": "running",
        "timestamp": "2024-01-01T00:00:00Z"
    }
}

// 统计数据更新
{
    "event": "stats.update",
    "data": {
        "entity_type": "pipe",
        "entity_id": "xxx",
        "bytes_in": 1024000,
        "bytes_out": 1024000,
        "bitrate": 2000000,
        "fps": 30
    }
}

// 日志输出
{
    "event": "log",
    "data": {
        "level": "info",
        "message": "Stream started",
        "timestamp": "2024-01-01T00:00:00Z",
        "fields": {}
    }
}
```

## 6. 项目结构与旧章节作废声明

> 以下原第 6–11 节内容已作废，不再作为实现依据，仅保留第 4 节（数据库设计）与第 5 节（API 设计）作为契约参考：
>
> - 原第 6 节（项目结构）已过时，当前结构以代码库实际为准。
> - 原第 7 节（第一步实现计划）已被第 12 节实施计划取代。
> - 原第 8 节（非功能需求）核心指标保留：单机 ≥50 并发流、秒级延迟、流中断自动重连、配置持久化。
> - 原第 9 节（后续扩展）保留：HLS/GB28181/WebRTC/SRT、转码、录制、集群、负载均衡。
> - 原第 10 节（Docker 部署）以 `docker/Dockerfile` 与 `docker-compose.yml` 实际为准。
> - 原第 11 节（旧测试方案）已被第 13 节"测试方案：Mock 三角验证"取代。
## 12. 媒体管道标准（Media Pipe Standard）【定稿】

> 本节取代第 3 节以来所有关于媒体引擎的实现描述，是当前与未来的实现依据。

### 12.1 设计动机

早期实现偏离了"管道转发"设计：输入端退化为只做元数据探测，RTMP/HTTP-FLV 输出改为直接读文件（`StartOutputWithFile`），仅 RTSP 输出走 Go 包通路，且依赖手写 H.264/TS 解析与伪造时间戳（`videoPTS += 33000`）。这造成两条互相矛盾的数据通路、N+1 个 ffmpeg 子进程、RTSP 输入空壳、音频丢失。

**根本问题**：管道没有承担"中间标准"的职责，source 与 sink 通过具体实现直接耦合。

**本标准的定位**：pipe 是 source 与 sink 之间唯一的标准契约，包含 **码流子通道 / 码流帧 / 信令** 三部分。任何 source（MP4/RTSP/未来 HLS/GB28181）与任何 sink（RTMP/RTSP/HTTP-FLV/录制器）只需适配该标准，即可互相通信，互不感知对方的具体协议。

### 12.2 总体架构

```
   Source 适配层                          Pipe（标准契约）                         Sink 适配层
┌───────────────────┐   数据面（单向，高吞吐）   ┌──────────────────┐   数据面      ┌───────────────────┐
│ MP4 (mp4ff)       │ ────标准码流帧/子通道───► │  子通道           │ ────────────► │ RTMP（原生Go）     │
│ RTSP (gortsplib)  │                        │  码流帧（带PTS）   │               │ RTSP server      │
│ (未来: HLS/GB)    │ ◄──信令（双向，低频）──── │  信令              │ ◄──────────── │ HTTP-FLV         │
└───────────────────┘                        └──────────────────┘               │ 录制器            │
      媒体→标准                                           标准→媒体              └───────────────────┘
```

要点：
- source 和 sink 只认识标准，互不认识；新增协议 = 新增适配器。
- 数据面与控制面分离：数据面单向、高吞吐、低延迟；控制面双向、低频、可靠。

### 12.3 子通道

- 每条媒体流对应一个 `StreamID`/`ChannelID`（`uint8`），`Kind` ∈ `video` / `audio` / `data`。
- 保留 `control` 通道语义用于信令（见 12.5）。
- 订阅（`Subscribe`）时可选择只订阅部分子通道（如 HTTP-FLV 不需要字幕）。
- 子通道可动态增删：RTSP 源后到音轨、HLS 多层码流等场景，通过 `InfoUpdate` 事件通知。

### 12.4 码流帧（数据面）

帧为长度前缀二进制，自带自同步能力，天然可跨进程传输。

```
┌──────────┬──────────┬─────────┬──────────┬────────┬───────┬──────┬──────┬──────────┬─────────────┐
│ Magic 2B │Version 1B│ChanID 1B│FrameType │Codec 4B│Flags 1B│PTS 8B│DTS 8B│ PayloadLen│  Payload    │
│  0x584D  │   0x02   │         │  1B      │        │        │      │      │  4B       │             │
└──────────┴──────────┴─────────┴──────────┴────────┴───────┴──────┴──────┴──────────┴─────────────┘
                                    FrameHeaderSize = 30
```

- **一帧 = 一个 access unit**（H.264 一个 NAL 集合 / 一帧图像，AAC 一个原始帧）。
- **时间戳**：`PTS`/`DTS` 使用**流内原生 timescale**（`StreamInfo.ClockRate`：video 默认 90000，audio = 采样率），`int64` 无回绕。sink 按 `ClockRate` 换算到目标时钟：
  - RTMP/HTTP-FLV tag 时间戳（ms）= `PTS * 1000 / ClockRate`
  - RTSP H.264 RTP 时间戳（90kHz）= `PTS * 90000 / ClockRate`
  - RTSP AAC RTP 时间戳 = `PTS * SampleRate / ClockRate`
- **Flags**：`Keyframe`、`Config`（携带 CodecConfig，用于动态源）、`EOF`。
- 每个 access unit 的**精确切分由输入 demuxer 保证**（mp4ff 按 sample 输出；gortsplib client 按 RTP 解包），**Go 端不做任何 NAL/TS 手工解析**。

### 12.5 信令（控制面）

信封结构 `{Type, Payload}`，可序列化（进程内走 channel，进程间走同一编解码）。

**同步 request/response：**

| 消息 | 方向 | 用途 |
|------|------|------|
| `Subscribe(channels)` → `StreamInfo[]` | sink→source | 建立连接，返回媒体信息（子通道、Codec、SPS/PPS/AAC config、分辨率、帧率、ClockRate） |
| `Unsubscribe` | sink→source | 断开 |
| `Start` / `Pause` / `Resume` / `Stop` | sink→source | 流控 |
| `Seek(pts)` | sink→source | 文件源点播/录制重播 |
| `GetStreamInfo` → `StreamInfo[]` | sink→source | 查询 |

**异步事件：**

| 消息 | 方向 | 用途 |
|------|------|------|
| `InfoUpdate` | source→sink | 动态增删子通道/参数变化 |
| `StateChange` | source→sink | 状态迁移（started/stopped/error） |
| `Error` | source→sink | 错误通知 |

**典型能力**：
- **按需拉流**：RTSP server sink 收到客户端 DESCRIBE 时才向 source 发 `Subscribe`/`Start`，无观众时 source 不读文件。
- **录制重播**：录制器 sink 发 `Seek` 从头重录。
- **动态协商**：sink 只订阅自己支持的子通道。

### 12.6 CodecConfig

- `Subscribe` 返回的 `StreamInfo` 携带 CodecConfig：MP4 源从 `avcC` box 取 SPS/PPS、从 `esds` 取 AudioSpecificConfig；RTSP 源从 SDP 取。
- 同时保留 `Config` 标志帧能力，用于动态源在运行中才拿到配置的场景。
- RTMP/HTTP-FLV 的 sequence header 由 sink 从 CodecConfig 生成，不在数据面逐帧携带。

### 12.7 拓扑与背压

- **pipe = 1 source ↔ 1 sink** 的连接。
- **fan-out（一对多）** 由 source 端管理多个 sink：每个 sink 独立缓冲队列 + 独立慢消费/丢帧策略，一个慢 sink 不阻塞其他 sink，也不阻塞输入 readLoop。
- 缓冲满时丢帧并计数/上报，输入侧永不阻塞。

### 12.8 适配器

**输入（媒体 → 标准）**
- MP4 文件：`mp4ff`（`github.com/edgeware/mp4ff`）按 sample 输出，携带真实 PTS/DTS/keyframe，AVCC→AnnexB 在输入侧完成。
- RTSP：gortsplib v5 client（mediamtx 同款）拉流 + RTP depacketize → 标准帧。
- 范围限定 MP4（H.264/H.265 + AAC）+ RTSP；非 MP4 文件先明确报错拒收，未来再以 ffmpeg 兜底。

**输出（标准 → 媒体）**
- RTSP server：gortsplib + rtph264/rtpaac 封装（沿用现有实现，修正时间戳单位换算）。
- HTTP-FLV：Go 写 FLV tag → HTTP 流式响应；与 RTMP 共享 FLV tag 编码（`flv_writer`）。
- RTMP（推流）：原生 Go 实现握手（C0/C1/C2）、chunk（协商 chunk size）、AMF0 connect/metadata、24 位时间戳回绕；数据面即 FLV tag 流。

**进程内/进程间**
- 进程内：数据面 Go channel 广播，控制面同步调用（带超时）。
- 进程间：同一帧/信封编解码走 unix socket/TCP + 长度前缀封装，两端复用相同标准，未来扩展。

### 12.9 实施清理清单【已完成】

已删除：
- `ts_demuxer.go`（524 行 TS 解复用）、`muxer.go`、`demuxer.go`
- 旧 `file_input.go` / `rtsp_output.go` / `rtmp_output.go` / `httpflv_output.go`（直读文件 + 伪造时间戳）
- 旧 `engine.go` / `default_engine.go`（旧 InputStream/OutputStream/MediaEngine 接口）
- `StartOutputWithFile` 旁路、`MediaPacket`、`PacketHandler`、所有 `[TRACE-*]`/hex dump/"SAFETY VALVE" 调试日志

已实现（替代旧架构）：
- `pipe.go`：`Source` / `Sink` / `Pipe` 标准接口
- `frame.go` / `signal.go` / `clock.go`：标准帧、信令、ClockRate 换算
- `mp4_source.go`：MP4（mp4ff）输入，真实 PTS/DTS/关键帧，AVCC→AnnexB
- `rtsp_input.go`：RTSP 输入（gortsplib v5 client），RTP depacketize
- `default_pipe.go`：管道实现（fan-out / 背压 / 信令路由）
- `rtsp_sink.go` / `httpflv_sink.go` / `rtmp_sink.go`：各输出适配器
- `media_hub.go`：媒体引擎（`Engine` 接口 + `MediaHub` 实现），服务层接线
- 新增依赖：`github.com/Eyevinn/mp4ff`

### 12.9.1 L4 端到端验收结果【实测】

本地部署全链路验证（ffmpeg/ffprobe 拉流）：
- 文件→RTSP server：**通过**（h264 1920x1080 + aac 32000Hz 拉流解码）
- 文件→HTTP-FLV：**通过**（ffmpeg 实时拉流解码输出帧；需 `Transfer-Encoding: chunked`）
- 文件→RTMP：协议层经 RTMP mock server 完整验证（握手/AMF0/消息）
- MP4→RTSP→RTSP 中继：**通过**（L3 集成测试）

已知边界：
- **文件循环播放（loop=true）**：已实现"统一基准回绕"方案——以绝对时长最长的 track 为基准，回绕时各 track 的 `loopDur` 换算到同一绝对时间轴（首帧对齐），保证回绕后音视频时间戳单调、不漂移。单测 `TestMP4Source_AlignLoopAlignment` 验证绝对时间轴一致。
- **HTTP-FLV GOP 重放**：新客户端从最近关键帧（GOP 起点）开始拉流，若从流中段接入可能从半截 tag 起播，ffmpeg 实时拉取 loop 流时可能报 `Packet mismatch` 或 `frame=0`（loop=false 正常）。这是 GOP 重放的已知兼容性边界，不影响 RTSP/HTTP-FLV 单次播放。
- **时间戳溢出**：标准帧用 int64（无实际风险）；FLV/RTMP 32 位时间戳约 49.7 天；RTSP RTP 32 位约 13.3 小时回绕（RFC 允许，播放器按增量处理）。只要保证回绕瞬间 PTS 增量连续，影响可控。

### 12.10 非目标（本期不做）

- 转码（保留 ffmpeg 转码作为未来模块，不在数据通路上）。
- 录制、集群、负载均衡。
- 非 MP4 容器输入（FLV/TS 文件）与 H.265 之外的多编解码深度适配（mp4ff 对 H.265 支持良好，本期以 H.264 + AAC 为验收基线）。

---

## 13. 测试方案：Mock 三角验证【定稿】

### 13.1 核心思想

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

### 13.2 Mock 三件套

- **MockSource**：按测试剧本可编程产帧（合法 H.264 AU、AAC 帧），响应信令（`Subscribe`→回预设 `StreamInfo`、处理 `Seek`/`Pause`），记录收到的控制请求。
- **MockSink**：接收帧并断言（顺序/字节/时间戳/关键帧），可发起信令请求。
- **MockPipe**：内存双向通道——真实 source 可推帧、真实 sink 可消费，信令 request/response + 事件两路齐全。

### 13.3 四层测试金字塔

**L1 契约层（纯单元，无 mock）**
- FrameHeader 编解码 round-trip、30 字节边界、magic/version 校验、截断/损坏
- 信令 Envelope 编解码 round-trip
- ClockRate 换算表驱动（→ms、→90kHz，视频/音频不同 timescale）

**L2 组件层（真实 + 两 mock）——核心**

| 被测组件 | MockSource | MockPipe | MockSink | 关键断言 |
|---|---|---|---|---|
| MP4Source | ✓ | ✓ | | Subscribe 返回正确 SPS/PPS/AAC config/ClockRate；首帧=keyframe；每 AU 一个 sample；PTS/DTS 与 mp4ff 一致且单调；音视频交错；Loop/Seek/Pause/Stop 语义 |
| RTSPInput | ✓ | ✓ | | 拉流→depacketize→标准帧内容正确；动态 InfoUpdate（音轨后到） |
| Pipe | ✓ | ✓ | | 信令路由、帧转发（顺序/字节一致）、fan-out 双 sink 独立全量、背压丢帧计数且不阻塞 source、Unsubscribe 停收、事件下发 |
| RTSP server sink | ✓ | ✓ | | StreamInfo→正确 SDP/format；rtph264 封装字节级校验；ClockRate 换算 |
| HTTP-FLV sink | ✓ | ✓ | | flv header + sequence header（avcC/ASC）+ tag 头（type 8/9、时间戳 ms、CTS）字节级校验 |
| RTMP sink | ✓ | ✓ | | 握手 C0/C1/C2、connect AMF0、sequence header、tag 时间戳、24 位回绕、断线重连 |

**L3 半集成（两个真实 + 一个 mock）**
- 真实 MP4Source + 真实 Pipe + MockSink：标准帧在真实链路无损
- MockSource + 真实 Pipe + 真实 RTMP sink：标准→协议字节
- 真实 MP4Source + 真实 Pipe + 真实 HTTP-FLV sink：离线端到端（flv 落文件/内存校验）

**L4 全真端到端（需外部依赖，手动/CI 可选）**
- file→rtmp/rtsp/http-flv 用 ffprobe/ffplay 实测（扩展 `server/test/integration/api_test.sh`）

### 13.4 测试数据策略

- **合成字节 fixture**（确定性、离线、毫秒级）：合法 SPS/PPS/IDR 字节 + AAC 帧 + AudioSpecificConfig，用于协议封装测试。
- **真实文件 fixture**（`server/test/fixtures/test.mp4`，已在仓库）：用于 MP4Source 的 PTS/DTS/交错/Loop 断言。
- 两种并存：合成字节测协议封装，真实文件测 demux 正确性。

### 13.5 RTMP mock server

原生 RTMP sink 测试需要最小 RTMP server（握手 C0/C1/C2 + chunk 解析 + AMF0 假服务端）接收字节并断言，约 200 行，属于测试基建的一部分。确保 RTMP sink 可离线完整验证。

### 13.6 时序处理约定

- 流式异步一律用 channel + 超时等待（`require.Eventually` / 带 deadline 的 select），**禁用 `time.Sleep` 猜测时序**，减少 flaky。
- 并发相关的断言放在接收 goroutine 内完成后再退出，避免竞态。

### 13.7 与现有测试的关系

现有 `MockInputStream`/`MockOutputStream`/`MockMediaEngine`（绑定旧接口）随新架构替换为 `MockSource`/`MockSink`/`MockPipe`。服务层测试（CRUD/校验）保留 mock repo 模式不变。

### 13.8 实施顺序

按 **L1 契约层 → L2 组件层（MP4Source → Pipe → RTSP server sink → HTTP-FLV sink → RTMP sink → RTSPInput）→ L3 半集成 → L4 端到端** 顺序，每完成一个组件即用 mock 三角验证，再进入下一个。
