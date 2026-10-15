# GB28181 Sink（虚拟摄像头）设计方案

> 状态：**待评审** ｜ 版本：v0.1 ｜ 日期：2026-10-15 ｜ 相关代码：`server/internal/media/`、`server/internal/service/`、`server/internal/model/`、`web/src/views/Outputs.vue`

---

## 0. 评审重点（TL;DR）

1. **技术选型（§6）**：推荐"方案 A：引入 `github.com/mickeyzzc/gb28181-go`（MIT，依赖极轻，生产验证）"包裹在本地适配层内；其中 **信令层用官方 `device` 包、媒体层用同库 `psmux`**（支持 H264/H265 + AAC/G711）——需要内置 fork 做最小改造（见 §6.2/§13-Q1）。
2. **一期范围（§1.2/§1.3）**：单通道虚拟 IPC；**视频 H.264 + 音频 AAC**（AAC 直封 PS，**无需转码**，平台原生支持）。H.265 亦已就绪（PSM 0x24），是否纳入一期见 Q2。
3. **附带成本**：`go 1.26` 升级（本机已就绪）+ Docker builder 镜像升级（§6.2、§8）。
4. **联调目标平台**：建议 wvp-GB28181-pro（+ZLMediaKit）作为基准（§11.4、Q6）。
5. 其余决策点集中在 **§13 开放问题表**，请逐项给出结论后即可按 §12 里程碑开工。


---

## 1. 背景与目标

### 1.1 需求

新增一种输出端类型 **GB28181 Sink**：x-media 作为一台"虚拟摄像机"（GB28181 设备，UAC 角色）注册到 GB/T 28181 视频监控平台，并响应平台下发的各类指令（目录查询、设备信息、心跳、点播邀约等）。平台"预览"该摄像机时，x-media 把管道中的视频流以 **PS over RTP** 推给平台，平台侧即可像看真实摄像机一样出图。

典型场景：

```
MP4 文件 / RTSP 源 ──► x-media 管道 ──► [GB28181 Sink] ──SIP──► GB28181 平台（注册/心跳/目录/邀约）
                                          └──RTP(PS)──► 平台媒体服务器（ZLM 等）──► 平台 Web 出图/录像/转码
```

### 1.2 v1 范围

| 维度 | v1 支持 | 说明 |
|---|---|---|
| 设备形态 | 单设备单通道（虚拟 IPC） | DeviceID 20 位；ChannelID 默认 = DeviceID |
| SIP 信令 | REGISTER（摘要认证）、MESSAGE（心跳/目录/信息/状态/配置/控制/录像查询）、SUBSCRIBE（应答）、INVITE/BYE/CANCEL/ACK、OPTIONS、INFO | UDP 为主，TCP（含 tcp-out）可配 |
| 媒体 | **H.264/H.265 视频 + AAC 音频**（AAC 直封 PS，**无需转码**），PS 封装 → RTP，UDP 与 TCP（主动/被动） | H265 纳入范围见 Q2；跟随平台 INVITE SDP 的传输方式 |
| 指令响应 | 目录、设备信息、设备状态、配置下载、设备控制（PTZ/录制/布防/校时等按策略应答）、录像查询（空）、订阅、心跳 | 见 §5.4 应答矩阵 |
| 平台版本 | GB/T 28181-2016 线格式（附带 2022 兼容开关：`X-GB-Ver` 头、校时） | 目标平台：wvp-GB28181-pro 优先，兼容海康/大华/宇视平台 |
| 输出端语义 | 注册到平台 + 被动受邀推流（push sink，无对外拉流地址） | 与 RTMP/RTSP push 语义相近 |

### 1.3 v1 非目标（列入后续里程碑，见 §12）

- 音频转码：**不需要**——国标平台原生支持主流编码，AAC 可直接封装进 PS 播放（§5.6），既无需转 G.711，也无需 ffmpeg
- H.265 源编码：平台与 PS 封装（PSM 0x24）均支持，待 Q2 决定是否纳入一期
- 录像回放/下载（Playback/Download）、语音对讲/广播、报警上报、级联、国密 GB35114、TLS/SIPS
- 多通道设备（一台设备挂多路流）——v1 单通道，多实例可各自注册为独立设备

### 1.4 验收标准（摘要）

1. 平台（以 wvp-GB28181-pro 为基准）设备列表出现该设备，**在线**；心跳持续、断网恢复后自动重注册。
2. 平台查询目录/设备信息/设备状态，应答内容正确。
3. 平台点播：**2 秒内出图**（首帧为 IDR），音视频连续无花屏、音频非静音；ffmpeg 从平台侧拉流（RTMP/RTSP/HLS 任一路）可正常解码（含 AAC 音频）。
4. PTZ 与控制类指令有正确 SIP 应答（无物理云台时按策略应答 OK）。
5. 输出端"停止"后：主动注销，平台状态转离线；重启后可再次注册。
6. 自测：新增单测 + 组件测试（内置 FakePlatform）全绿；全量 `go test ./...` 无回归。

---

## 2. 术语与协议速览

| 术语 | 含义 |
|---|---|
| UAC / 设备 | 本方案中 x-media 的角色（SIP 客户端 + 媒体发送端） |
| 平台 | GB28181 监控平台（SIP 服务端，通常由 wvp 等实现；媒体接收由 ZLMediaKit 等承担） |
| MANSCDP | 国标"监控报警联网系统控制描述协议"，SIP MESSAGE 承载的 XML 消息 |
| PS / PS 流 | MPEG-2 Program Stream，国标媒体封装格式；RTP 负载类型 96（`PS/90000`） |
| SSRC | RTP 同步源标识；平台在 INVITE SDP 的 `y=` 字段给出，设备发送时采用 |
| SN | 消息序列号（XML 内），请求-应答配对 |
| MANSRTSP | 回放控制信令（二期），SIP INFO 承载 |

协议栈全景：

```
信令（SIP over UDP/TCP）       媒体（RTP，PT=96）
┌────────────────────┐        ┌─────────────────────────────┐
│ REGISTER  注册      │        │ PS（pack+system+PES）        │
│ MESSAGE   心跳/查询  │        │  └► RTP 分包（≤1400B/包）    │
│ SUBSCRIBE 订阅      │        │       UDP 直发 / TCP(2B 长度)│
│ INVITE    点播      │        └─────────────────────────────┘
│ ACK/BYE   会话      │
└────────────────────┘
```

---

## 3. 现状与集成点

### 3.1 x-media 管道标准（现有）

- `Source → DefaultPipe → Sink`：sink 实现 `Sink` 接口（`Start/Stop/Status/WriteFrame/Configure/Notify`，见 `server/internal/media/pipe.go`）。
- 管道启动顺序（`default_pipe.go:Start`）：`sink.Configure(streams)` → `sinkWriter` 循环 → `sink.WriteFrame(f)`；pipe 失败会回滚。
- 输出端工厂：`MediaHub.CreateOutput`（`media_hub.go:99`）按 `config.Type` 分发；服务层两处透传配置（`status_util.go:120`、`pipe_service.go:225`）。
- 已有可复用设施：
  - `splitAnnexB()`（`rtsp_sink.go`）：Annex-B 拆 NALU
  - `ConvertClock()`（`clock.go`）：PTS 时钟换算
  - `ClientInfoProvider`（`client_info.go`）：输出端详情页"当前客户端"
  - GOP 缓存重放（`rtsp_sink.go` 已实现，新会话即时出图的关键手段）
  - `StreamInfo.CodecConfig`（SPS/PPS/AVCC）；`TimestampStats` 时间戳健康度统计

### 3.2 接入点（本方案新增/改动的最外层）

```
service.OutputService ──► MediaHub.CreateOutput ──► NewGB28181Sink(config)
        │                        │                        │
   model.Output.Config     media.OutputConfig        device.Config（库）
   （JSON 校验/前端表单）   （字段透传）              （gb28181-go/device）
```

---

## 4. 总体设计

### 4.1 组件结构

```
┌─────────────────────────── server/internal/media（package media） ───────────────────────────┐
│                                                                                              │
│  Gb28181Sink（实现 Sink 接口，新文件 gb28181_sink.go）                                        │
│  ├─ 配置校验/状态机（stopped/running/error）                                                   │
│  ├─ FrameHub：帧适配 + fan-out（x-media Frame ─► 媒体层输入）                                  │
│  │    ├─ 视频：Annex-B 拆分（复用 splitAnnexB）→ NALU（SPS/PPS/IDR 标注）                       │
│  │    ├─ 音频：AAC 帧 + ADTS 头（参数取 CodecConfig/ASC；直封，无需转码）                        │
│  │    ├─ PTS(ClockRate) ─► 90kHz（会话基准单调）                                                │
│  │    ├─ 每订阅者有界 channel（容量 128，满则丢；关键帧优先）                                     │
│  │    └─ GOP 缓存重放（新订阅者先注入最近一个 GOP，保证出图）                                     │
│  ├─ MetricsHooks 适配：注册成功/失败、心跳失败、会话建立/结束、PS 字节数 → 日志/统计             │
│  └─ ClientInfoProvider：平台会话列表（SSRC/地址/传输/开始时间）                                  │
│                                                                                              │
│  device.Server（信令层：依赖或内置 github.com/mickeyzzc/gb28181-go/device，见 §6.2）            │
│  ├─ SIP 收发（UDP/TCP/tcp-out）＋ REGISTER 摘要认证（MD5，qop=auth 兼容）                       │
│  ├─ MANSCDP XML（心跳/目录/信息/状态/配置/控制/录像查询/订阅应答）                                │
│  └─ 心跳静默检测与自动重注册 / BYE/ACK/CANCEL 事务                                              │
│                                                                                              │
│  媒体层（A+：同库 psmux；A：device 内置 H264-only 路径）                                        │
│  ├─ PS 打包：pack/system header + PSM + PES（H264/H265 + AAC/G711）                             │
│  └─ RTP 发送：≤1400B 分包、marker 帧末、UDP / TCP（$-帧）；SSRC 取 INVITE y=                     │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

**设计要点：库引用集中在两个边界**（依赖形态=仅 `gb28181_sink.go`；内置形态=额外 `server/internal/gb28181/`），上层（service/前端）与媒体管道完全不感知库的存在；后续替换/自研只需重写适配层（接口边界 = `Sink` + `ClientInfoProvider`）。

### 4.2 生命周期状态机

```
                Start()                         INVITE(平台点播)
  stopped ──────────────► registering ──200──► running ◄──────────┐
     ▲                       │  ▲                │  │            │
     │  Stop()               │  │ 平台静默/断网     │  │ BYE        │
     │                       │  └──自动重注册──────┘  ▼            │
     │                       │ 403/认证失败        running（无会话）│
     │                       ▼                      │             │
     └─────────────────── error（前端可见）◄─────────┘ 会话建立即时出图
  Stop()：尽力注销（REGISTER Expires:0, 2s 超时）→ 库 Stop → 清理订阅 → stopped
```

说明：
- 注册发生在 `Sink.Start()`（此时可能还没接管道）——与媒体解耦：**先上线，后受邀**。
- `pipe.Start` 时调用 `Configure(streams)`；若源编码超出范围（如纯音频源、或 Q2 未纳入时的 H.265 源）则返回错误（管道启动失败，前端可见明确原因）；H.264 + AAC 源正常接受（音频与视频一并推流，直封 PS 无需转码）。
- 平台会话（INVITE）可以发生在任意时刻，可能同时多于 1 个（例如实况 + 另一路转发）；策略见 §5.7。

### 4.3 并发模型

| goroutine | 职责 | 退出条件 |
|---|---|---|
| `device.Server.Start` 主循环（库内） | SIP 收发、注册/心跳/重注册、INVITE/BYE | ctx 取消或 Stop |
| 库内每会话发送 goroutine | PS 打包 + RTP 发送 | BYE / 会话超时 / Stop |
| `Gb28181Sink` 帧适配 | `WriteFrame` 内同步转换 + 非阻塞扇出（不新开 goroutine） | — |
| 库内每会话 FrameSource 订阅协程 | `Subscribe(ctx)` 注册/注销 | ctx Done（BYE） |

**背压约定（沿用本仓库管道哲学）**：`WriteFrame` 永不阻塞；订阅 channel 满时丢帧并计数（关键帧优先、时间新鲜度优先）。

### 4.4 关键时序：注册 & 点播

```
设备(x-media)                              平台
    │  REGISTER (Expires 3600) ───────────►│
    │  ◄──────────── 401 + WWW-Authenticate │
    │  REGISTER + Authorization(Digest) ───►│
    │  ◄──────────── 200 OK                 │
    │  MESSAGE Keepalive（每 60s）─────────►│   ← 平台 3 个周期无心跳则判离线
    │  ◄──────────── 200 OK                 │
    │  ◄─────────── MESSAGE Query/Catalog   │   （平台上线即查目录）
    │  MESSAGE Response/Catalog ───────────►│
    ...
    │  ◄─────────── INVITE（SDP recvonly, y=SSRC）
    │  100 Trying / 200 OK（SDP sendonly）─►│
    │  ◄─────────── ACK                     │
    │  ══ RTP/PS 视频流（首帧 IDR）══════════►│   ← 平台媒体服务器收流出图
    │  ◄─────────── BYE                     │
    │  200 OK ─────────────────────────────►│   会话结束
```

---

## 5. 协议与流程设计

> 以下报文示例与字段口径均以 **wvp-GB28181-pro 实际代码** 与 gb28181-go 设备端实现为参照（wvp 的 `SIPCommander` 生成 offer、`InviteRequestProcessor.createSendSdp` 生成设备侧 answer）。

### 5.1 注册与摘要认证（REGISTER）

**流程**：`REGISTER(无认证)` → `401 + WWW-Authenticate` → `REGISTER(带 Authorization)` → `200 OK`。
- 摘要算法：MD5；兼容 `qop=auth`（带 `nc/cnonce`）与无 qop（旧平台）；
- 认证失败（403）：进入 error 状态 + 周期重试（退避由库控制），日志给出明确原因；
- 注册周期：默认 3600s，**以平台响应 `Expires` 协商**，提前刷新；平台重启/静默自动重注册（库内置静默检测）；
- 注销：`REGISTER(Expires: 0)` 带认证，Stop 时尽力执行。

报文示例：

```sip
REGISTER sip:34020000002000000001@3402000000 SIP/2.0
Via: SIP/2.0/UDP 192.168.88.150:15060;rport;branch=z9hG4bK...
From: <sip:34020000001320000001@3402000000>;tag=...
To: <sip:34020000001320000001@3402000000>
Call-ID: ...
CSeq: 1 REGISTER
Contact: <sip:34020000001320000001@192.168.88.150:15060>
Max-Forwards: 70
User-Agent: x-media/...
Expires: 3600
Content-Length: 0
```

本地 IP 探测：库以"到平台的 UDP socket"路由探测本地出口 IP（Via/Contact/SDP 用同一地址）；跨网/NAT 场景可显式配置 `local_ip`。

### 5.2 心跳（Keepalive）

设备→平台 MESSAGE（`Content-Type: Application/MANSCDP+xml`），默认 60s：

```xml
<?xml version="1.0" encoding="GB2312"?>
<Notify>
<CmdType>Keepalive</CmdType>
<SN>17</SN>
<DeviceID>34020000001320000001</DeviceID>
<Status>OK</Status>
</Notify>
```

- 平台回 SIP `200 OK`（不解析正文）；连续 `heartbeat_timeout_count`（默认 3）次失败或平台静默 → 自动重注册；
- XML 输出声明 `GB2312`（正文 ASCII/UTF-8，兼容国标设备习惯）；**入方向自动识别 UTF-8/GBK/GB18030**，中文设备名等无乱码问题。

### 5.3 查询类指令（平台 MESSAGE → 设备应答 MESSAGE）

| 指令 | 请求 `CmdType` | 设备应答 | 说明 |
|---|---|---|---|
| 目录查询 | `Catalog` | `Response/Catalog` | 单通道：`SumNum=1`，`DeviceList Num="1"`；大目录自动分页（库内置） |
| 设备信息 | `DeviceInfo` | `Response/DeviceInfo` | DeviceName/Manufacturer/Model/Firmware/Result |
| 设备状态 | `DeviceStatus` | `Response/DeviceStatus` | `Online=ONLINE`、`Status=OK`、`Encode=ON`、`Record=OFF`、`DeviceTime` |
| 配置下载 | `ConfigDownload` | `Response/ConfigDownload` | BasicParam（可选）+ 下载速度/分辨率能力（可选） |
| 录像查询 | `RecordInfo` | `Response/RecordInfo` | v1 无录像：`SumNum=0`、空列表 |
| 目录订阅 | SUBSCRIBE(Event: Catalog) | `200 OK`（回显 Expires） | NOTIFY 主动推送列入 M5 可选 |

目录应答示例（单通道 IPC 形态）：

```xml
<?xml version="1.0" encoding="GB2312"?>
<Response>
<CmdType>Catalog</CmdType>
<SN>1</SN>
<DeviceID>34020000001320000001</DeviceID>
<SumNum>1</SumNum>
<DeviceList Num="1">
<Item>
  <DeviceID>34020000001310000001</DeviceID>
  <Name>X-Media 虚拟摄像头</Name>
  <Manufacturer>x-media</Manufacturer>
  <Model>gb28181-sink</Model>
  <Owner>Owner</Owner>
  <CivilCode>340200</CivilCode>
  <Address></Address>
  <Parental>0</Parental>
  <ParentID>34020000001320000001</ParentID>
  <SafetyWay>0</SafetyWay>
  <RegisterWay>1</RegisterWay>
  <Secrecy>0</Secrecy>
  <Status>ON</Status>
</Item>
</DeviceList>
</Response>
```

### 5.4 控制类指令（DeviceControl）应答矩阵

授权策略：**默认全应答 `Result=OK`（记录日志），可通过配置改为"未实现即拒绝（ERROR）"**；有真实副作用的三类（重启/录制/强制 I 帧）默认策略如下：

| 子指令 | 含义 | v1 行为 | 可配置 |
|---|---|---|---|
| `PTZCmd` | 云台控制（8 字节位串） | 解码记录，应答 OK（无物理云台） | 回调（未来接 AI/虚拟 PTZ） |
| `IFrameCmd` | 请求强制 I 帧 | 向 Source 发"请关键帧"信令（源不支持则忽略），应答 OK | — |
| `RecordCmd` | 开始/停止录制 | 应答 OK（无本地录像能力；平台侧录像由平台承担） | 可改拒绝 |
| `GuardCmd` / `AlarmCmd` | 布防/撤防、报警复位 | 应答 OK | 可改拒绝 |
| `TeleBoot` | 远程重启 | **默认拒绝（ERROR）**，不重启进程 | 可开放为重启 |
| `DeviceConfig`（BasicParam 等） | 参数配置下发 | 记录并应答 OK；心跳周期等不热更新（v1） | 可开放热更新 |

### 5.5 点播会话（INVITE / ACK / BYE）

**平台 offer（典型，wvp UDP）**：

```sdp
v=0
o=34020000002000000001 0 0 IN IP4 192.168.88.100
s=Play
c=IN IP4 192.168.88.101        ; 平台的媒体接收地址（ZLM）
t=0 0
m=video 30100 RTP/AVP 96       ; TCP 时为 "TCP/RTP/AVP 96"
a=recvonly
a=rtpmap:96 PS/90000
y=0100000014                    ; 平台指定的 SSRC（设备发送时必须采用）
; TCP 时附加：a=setup:passive|active , a=connection:new
```

**设备 answer（200 OK）**：

```sdp
v=0
o=34020000001320000001 0 0 IN IP4 192.168.88.150
s=Play
c=IN IP4 192.168.88.150
t=0 0
m=video 32000 RTP/AVP 96       ; 设备媒体端口（TCP 被动时平台将连此端口）
a=sendonly
a=rtpmap:96 PS/90000
y=0100000014                    ; 回显平台 SSRC（缺失时自生成）
f=
```

**传输协商规则**（RFC 4145 语义，与 wvp 的 `TCP-PASSIVE/TCP-ACTIVE/UDP` 三种模式对应）：

| 平台 offer | 设备 answer | 行为 |
|---|---|---|
| `RTP/AVP`（无 setup） | `RTP/AVP` | 设备用本地 UDP socket 直发 `c=` IP + `m=` 端口 |
| `TCP/RTP/AVP` + `a=setup:passive` | `a=setup:active` | 平台监听，**设备主动 TCP 连接**其 `c=`/`m=` |
| `TCP/RTP/AVP` + `a=setup:active` | `a=setup:passive` | 设备监听 `m=` 端口，等待平台连接 |
| TCP 媒体帧格式 | — | `$` 帧：`0x24` + channel(1B) + 长度(2B 大端) + RTP 包（附录 C.2；已核对 ZLMediaKit `RtpSplitter` 按此解析） |

**事务健壮性**：重复 INVITE（未收到 ACK 的平台重传）→ 幂等重发 200；BYE → 200 并停止会话；CANCEL（200 尚未发出）→ 200 + 对 INVITE 回 487；ACK 未到即重传 200（库内事务处理）。

### 5.6 媒体封装（PS → RTP）

**PS 结构**（每帧一个 PS 突发）：

```
┌─────────────────────────────────────┬──────┬───────────────────────────────────────┐
│ ① PS pack header  00 00 01 BA       │ 14B  │ SCR（PTS/300 派生）+ program_mux_rate    │
│ ② System header   00 00 01 BB       │ 视长  │ 流列表（视频 0xE0）；IDR 帧发送           │
│ ③ PSM             00 00 01 BC       │ 视长  │ stream_type: 视频 1B/24、音频 0F/90(aac) │
│                                      │      │ 附 CRC32/MPEG-2；IDR 帧发送              │
│ ④ PES (视频)      00 00 01 E0       │ 变长  │ 80 80 05 + PTS[+DTS] + Annex-B NALU      │
│ ⑤ PES (音频)      00 00 01 C0       │ 变长  │ PTS + [ADTS 头]+AAC 帧 / G.711 裸帧       │
└─────────────────────────────────────┴──────┴───────────────────────────────────────┘
```

- 视频 PES 负载 = **Annex-B NALU**（含 `00 00 01` 起始码）；IDR 帧前保证带 SPS/PPS/VPS（来自 `StreamInfo.CodecConfig`，不依赖源在带内提供）；
- 音频 PES 与视频 PES **同处一个 PS 突发**（规则见 §5.7）；
- PTS/DTS 以 **90kHz** 写入 PES 头（音频同理）；
- 一帧（AU）的 PS 突发 = 一个（或一组）RTP 包，**marker 界定突发末尾**；
- AU 过大时 PES 按 ≤60000 字节切分（续包无 PTS），保持 `PES_packet_length` 可表示。

**RTP 打包规则**（表内每条都是踩过坑的规范红线）：

| 项 | 规则 |
|---|---|
| PayloadType | **96**（SDP `a=rtpmap:96 PS/90000`） |
| SSRC | 优先 INVITE `y=`；缺失/为 0 时自生成 |
| Timestamp | 帧 PTS 换算 90kHz，帧间单调递增（会话基准对齐） |
| Sequence | 会话内逐包 +1，初始随机 |
| **Marker** | 一帧的**最后一个** RTP 分包置 1（其余 0）——同类问题此前已在 RTSP AAC 上踩坑（live555 依赖 Marker 交付帧），国标平台虽多按 PS 起始码重组，也一律按规范设置 |
| MTU | RTP 负载 ≤ 1400 字节（可配，默认 1400） |
| TCP | **`$` 帧（GB/T 28181 附录 C.2，RTSP-interleaved 风格）：`0x24 \| channel(1B) \| 长度(2B 大端) \| RTP 包`**；已核对 ZLMediaKit `RtpSplitter` 对 `data[0]=='$'` 按 4 字节头解析（即 wvp/ZLM 平台的实际期望格式） |

### 5.7 帧适配与多会话 fan-out

x-media 标准帧 → 媒体层 `device.AccessUnit` / psmux 输入的映射：

| x-media Frame | 媒体层输入 |
|---|---|
| `Kind=video` + H.264/H.265，Payload=Annex-B | `splitAnnexB` → NALU 列表；`KeyFrame` 取自 `FlagKeyframe`；IDR 前保证带 SPS/PPS/VPS（来自 `StreamInfo.CodecConfig`） |
| `Kind=audio` + AAC，Payload=raw AAC 帧（AU） | **加 7 字节 ADTS 头**（参数由 ASC 解析：profile/采样率索引/声道数；长度=帧长+7）→ psmux 音频帧；无需转码 |
| `PTS`（int64，ClockRate=90000/采样率） | PES PTS 换算 **90kHz**（音频 1024 样本 @44.1k ≈ 2089 tick/帧）；会话基准单调 |
| 其他编码 | `Configure` 阶段拒绝（明确错误），见 §13-Q2 |

**音频入 PS 的硬性规则**（psmux 作者在真实平台踩坑后的结论，直接引用）：

1. PSM（`00 00 01 BC`）声明流类型：视频 H264=`0x1B`/H265=`0x24`，音频 AAC=`0x0F`/G711A=`0x90`；音频 PES 用 stream_id `0xC0`；
2. **AAC 帧必须带 ADTS 头**（ZLMediaKit 侧 PS 复用对此有强校验：`MpegMuxer::inputFrame` 对 CodecAAC `CHECK(frame->prefixSize(), "required aac frame with adts header")`）；
3. **音频 PES 必须并入视频 AU 的同一 PS 突发（burst）内**（`AppendAudioPES`），不能单独成包——GB28181 是单条 RTP 流，marker 界定 AU 边界；独立发送音频会携带自身 marker，接收端会把视频帧截断（实测表现为 IDR 在 ~maxPESPayload 处断裂）。

GOP 重放（**首帧质量关键**）：缓存最近一个 GOP（自最近 IDR 起，容量 4MB/2s 上限）；平台 INVITE 建立订阅时，先把缓存 GOP 注入该订阅者的 channel，再续实时帧——平台**立刻收到 IDR 出图**，无需等下一个关键帧。

多会话：每会话独立 RTP（各自 SSRC/端口/序号）；PS 打包可按帧一次复用、各会话仅分包发送（优化项，v1 先按库默认每会话独立处理）。平台断开（BYE/超时）不影响其他会话。

### 5.8 停止与注销

`Sink.Stop()` 顺序：停止收帧 → 尽力 `Deregister(ctx, 2s)`（`REGISTER Expires:0`）→ 库 `Stop()`（关 SIP socket/会话）→ 清理订阅者 → `status=stopped`。
服务层级联（`output_service.Stop`）会先 `RemoveOutput` 并断开管道，与现有输出端一致。

---

## 6. 技术选型（评审决策点）

### 6.1 候选方案

| 维度 | **A：引入 `gb28181-go`（推荐，含 A+ 变体）** | B：完全自研 | C：sipgo + 自研媒体/XML |
|---|---|---|---|
| SIP 信令 | 现成：摘要认证(qop)、UDP/TCP/tcp-out/TLS、事务/重传、静默检测重注册 | 自研 ~1000+ 行，平台差异坑多 | sipgo 事务层，GB 语义仍需自研 |
| MANSCDP XML | 现成：全命令面、分页、GBK 自动识别 | 自研 ~400 行 | 自研 ~400 行 |
| PS/RTP | `device` 包现成（H.264 视频）；同库 `psmux` 包支持 **H264/H265 + AAC/G711**（PSM/CRC/超长切分/音频并入突发均有实测注释） | 自研 | 自研 |
| 控制指令面 | 现成（PTZ 位串解码、配置、快照、对讲等回调） | 全自研 | 全自研 |
| H.265 | `device` 包未支持；换用 `psmux` 媒体层即具备（PSM 0x24） | ✓ | ✓ |
| 音频 | `device` 包未支持；换用 `psmux` 媒体层即具备（AAC 直封，ADTS；PSM 0x0F） | ✓ | ✓ |
| 依赖 | MIT，仅 pion/rtp + x/text（本仓库已有），**无 CGO** | 0 | sipgo + 其依赖链 |
| Go 版本 | 模块要求 go 1.26（本机已 1.26.5；Docker 镜像需升级） | 无要求 | 视库 |
| 联调风险 | 低（生产验证 + 自带上万行测试与 loopback conformance） | 高（每家平台 quirks） | 中 |
| 可控性 | 中（v0.x API 未冻结 → 用适配层隔离并锁版本） | 高 | 高 |

### 6.2 推荐

**推荐：方案 A+**（官方库信令层 + 同库 `psmux` 媒体层）：

1. **信令层用官方 `device` 包**："响应平台下发的各种指令"面很宽，其实现在真实平台验证过（摘要 qop、TCP 信令、事务重传、静默检测重注册、GBK 兼容），自研等价实现的工作量与联调风险不成比例；依赖极轻（closure 仅 pion/rtp、x/text，均已在树内）、MIT、无 CGO。
2. **媒体层用同库 `psmux` 包**：`device` 包内置媒体路径为 H264 视频 only（`server.go:1567` 直调 `MuxH264ToPS`，无扩展缝）；而 `psmux` 已支持 **H264/H265 + AAC/G711**（PSM/CRC32、超长 AU 切分、"音频必须并入视频 AU 突发"等实测经验均已成文）——替换媒体路径即可一期同时支持音频与 H265。
3. **落地形态（见 §13-Q1）**：
   - **A+（推荐）**：把 `device` + `manscdp` + `metrics` + `psmux` 四个包内置进仓库（`server/internal/gb28181/`，非测试合计 ~9.0k 行，保留 MIT 版权头与出处；`platform`/`security35114` 仅原库测试使用，无需内置），媒体调用点切换为 psmux；同时向上游提 PR，后续可用 patch series 跟随 upstream。
   - **A（保守备选）**：直接依赖官方 `v0.12.0`（H264 视频-only），音频/H265 等上游合入后再启用——不维护 fork，但一期功能缩水。
4. 适配层边界（`Sink` 接口）足够薄：无论 A/A+，业务语义只在 `gb28181_sink.go` 落地，未来切换方案 B 亦平滑。

代价与缓解：

- 模块要求 **go 1.26** → `server/go.mod` 升 `go 1.26`（本机 1.26.5 已就绪）；`docker/Dockerfile` builder 镜像升级 `golang:1.26-bookworm`（若内网 registry 未同步，用 `GOTOOLCHAIN=auto` 兜底）；
- v0.x API 未冻结 / fork 维护 → **锁定来源版本 v0.12.0**；改动面压到最小（PS 打包调用点、订阅分发两处）；外部引用集中在 `gb28181_sink.go` 一个文件。

---

## 7. 配置设计

### 7.1 输出端配置（JSON，存 `outputs.config`）

```json
{
  "platform_ip": "192.168.88.100",
  "platform_port": 15060,
  "device_id": "34020000001320000001",
  "channel_id": "34020000001310000001",
  "sip_domain": "3402000000",
  "password": "******",
  "transport": "udp",
  "local_sip_port": 0,
  "local_ip": "",
  "register_interval": 3600,
  "heartbeat_interval": 60,
  "heartbeat_timeout_count": 3,
  "protocol_version": "",
  "audio_enable": true,
  "device_name": "X-Media 虚拟摄像头",
  "manufacturer": "x-media",
  "model": "gb28181-sink",
  "firmware": "1.0.0",
  "gop_replay": true,
  "control_reject_unimplemented": false
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `platform_ip` / `platform_port` | 必填 | 平台 SIP 服务地址与端口（如 wvp 的 15060/5060） |
| `device_id` | 必填 | 20 位国标编码（设备身份，注册用户） |
| `channel_id` | = device_id | 通道编码（目录 Item 的 DeviceID） |
| `sip_domain` | 必填 | SIP 域（一般 = 设备 ID 前 10 位） |
| `password` | 必填 | 平台约定密码（摘要认证密钥） |
| `transport` | `udp` | 信令传输：`udp` / `tcp` / `tcp-out`（设备拨平台，保证 NAT 环境可用） |
| `local_sip_port` | 0 | 本地 SIP 端口；0 = 内核分配 |
| `local_ip` | "" | 空 = 自动探测出口 IP；跨网段时显式指定 |
| `register_interval` | 3600 | 注册有效期（秒），按平台响应协商 |
| `heartbeat_interval` | 60 | 心跳周期（秒） |
| `heartbeat_timeout_count` | 3 | 连续失败/静默多少次触发重注册 |
| `protocol_version` | "" | `X-GB-Ver` 头：""（省略）/`2.0`（2016）/`3.0`（2022） |
| `audio_enable` | true | 是否随视频推送音频（AAC 直封 PS）；关闭则纯视频通道 |
| `device_name` 等 | 见默认 | 目录/设备信息展示字段 |
| `gop_replay` | true | 新会话先注入最近 GOP（2 秒内出图） |
| `control_reject_unimplemented` | false | 未实现控制指令是否一律拒绝（ERROR） |

校验规则（`validateOutputConfig` 新增分支）：
- `device_id`/`channel_id`：必须 20 位数字；
- `platform_ip` 非空、`platform_port` 1~65535、`sip_domain`/`password` 非空；
- 间隔类字段范围（心跳 5~3600s、注册 60~86400s 等）；
- `transport` 枚举校验。

### 7.2 前端（`web/src/views/Outputs.vue`）

- 类型下拉新增 `GB28181`；表单字段：平台地址/端口、设备 ID、通道 ID、SIP 域、密码、信令传输、心跳/注册周期、（折叠）高级项；
- 列表"流地址"列：显示 `GB28181 · <channel_id>@<platform_ip>`（无对外拉流 URL）；
- 详情页：配置展示 + "平台会话"客户端列表（复用现有客户端表格：地址=平台媒体地址、传输=udp/tcp、UA=`gb28181:<ssrc>`）+ 运行状态。

---

## 8. 工程改动清单

| 文件 | 改动 | 里程碑 |
|---|---|---|
| `server/go.mod` / `go.sum` | A：新增依赖 `gb28181-go v0.12.0`；A+：无新依赖（pion/rtp、x/text 已在树）；`go 1.26` | M1 |
| `server/internal/gb28181/`（新增，仅 A+） | 内置 `device`/`manscdp`/`metrics`/`psmux` 四包（非测试 ~9.0k 行，保留 MIT 版权头与出处）；媒体调用点切换为 psmux（音频/H265） | M1/M3 |
| `server/internal/model/output.go` | `OutputTypeGB28181 = "gb28181"`；`OutputConfig` 新增 §7.1 字段 | M1 |
| `server/internal/media/engine.go` | `OutputConfig` 新增同名字段（透传） | M1 |
| `server/internal/media/gb28181_sink.go`（新增） | `Gb28181Sink`：Sink 接口 + FrameHub（视频/音频/ADTS/GOP 重放）+ ClientInfoProvider + MetricsHooks | M1-M3 |
| `server/internal/media/media_hub.go` | `CreateOutput` 增加 `case "gb28181"` | M1 |
| `server/internal/service/output_service.go` | `isValidOutputType` + `validateOutputConfig` 支持 gb28181 | M1 |
| `server/internal/service/status_util.go`、`pipe_service.go` | 两处 `media.OutputConfig` 映射补字段 | M1 |
| `server/internal/media/gb28181_sink_test.go`、`gb28181_fakeplatform_test.go`（新增） | L1/L2/L3 测试（§11） | M1-M4 |
| `web/src/views/Outputs.vue` | 表单/展示（§7.2） | M2 |
| `docker/Dockerfile` | builder 镜像 `golang:1.26-bookworm`（或 GOTOOLCHAIN 兜底） | M1 |
| `docs/design.md` | §12.8 适配器清单补 GB28181（完成后） | M4 |

---

## 9. 状态、日志与可观测性

- **Sink Status**：`running`（已启动；注册中/已注册均属正常过程）、`error`（认证被拒/持续注册失败）；
- **ClientInfo（输出端详情"客户端"列表）**：每个平台会话一条（地址、`udp/tcp`、会话开始时间、UA 含 SSRC）——看不到平台会话即意味着平台未点播；
- **应用日志**（INFO 级关键节点，WARN/ERROR 异常）：
  - `gb28181 registered (expires=3600)` / `register rejected: 403` / `platform silent, re-registering`
  - `invite accepted: channel=..., ssrc=0x..., transport=udp|tcp`
  - `session closed (bye)` / `deregistered`
- **计数（MetricsHooks → 日志/统计接口）**：注册尝试/成功/失败、心跳失败、会话开始/结束、PS 字节数；`WriteFrame` 侧丢帧计数（背压）。

---

## 10. 错误处理与边界场景

| 场景 | 行为 |
|---|---|
| 平台不可达 / 连续注册失败 | 状态 error（前端可见）+ 库内周期重试；恢复自动转 running |
| 密码错误（403） | error + 明确日志（不做无休止快速重试） |
| 平台静默（停止应答心跳） | 库内静默检测 → 自动重注册 |
| INVITE 到达但尚未接管道（无帧） | 200 OK 后等待帧（首帧随源启动到达）；记录 warn；平台侧一般有 10s 点播超时保护 |
| 源为 H.265（若 Q2 未纳入）/ 纯音频源 | `Configure` 返回错误 → 管道启动失败 → 前端报明确原因 |
| PTS 回绕 / 跳变 | 会话内以 90kHz 单调映射（沿用 `clock.go` 思路）；跳变 > 阈值时重置会话基准并记日志 |
| 无关键帧缓存（刚启动即被点播） | 收到下一个 IDR 后出图；`gop_replay` 降低该窗口 |
| 并发多个平台会话 | 独立 SSRC/端口/序号；BYE 只关本会话 |
| 平台携带组播地址（罕见） | 库支持设置组播 TTL；默认单播 |
| NAT/跨网段 | 信令用 `tcp-out` + `local_ip` 显式配置；媒体 UDP 需网络可达（v1 不支持媒体中继） |
| Stop 时平台不可达 | 注销尽力而为（2s 超时）后照常清理 |

---

## 11. 测试方案

沿用仓库"Mock 三角验证"（`docs/test-plan.md`）+ 红绿修复流程（先加红测试）。

### 11.1 L1 单元（纯函数/本地状态）

| 用例 | 说明 |
|---|---|
| 配置校验 | device_id/channel_id 20 位、必填、默认值、枚举 |
| 帧适配 | Annex-B 拆分、SPS/PPS/IDR 标注、KeyFrame 透传；AAC 帧 ADTS 头生成（采样率索引/声道/长度字段） |
| PTS→90kHz 映射 | 单调性、基准重置、音视频换算（90k/44.1k） |
| GOP 重放缓冲 | 订阅注入顺序、容量淘汰、关键帧优先丢帧策略 |
| SDP 解析/构造（适配层断言） | `y=`/`m=`/`c=`/`setup` 提取与 answer 字段 |
| 控制指令策略 | 默认应答/拒绝策略矩阵（teleboot 默认拒绝等） |

### 11.2 L2 组件（内置 FakePlatform + 真实 Gb28181Sink + MockSource 帧）

`FakePlatform`：进程内 UDP SIP 平台（测试文件内实现，~300 行）：401 质询并校验摘要、应答心跳 MESSAGE、发起 Catalog/DeviceInfo/DeviceStatus/ConfigDownload/RecordInfo/DeviceControl/SUBSCRIBE/OPTIONS、发起 INVITE（UDP/TCP 两种 SDP）并接收 RTP → 重组 PS 校验。

| 场景 | 断言 |
|---|---|
| 注册（401→200）/ 错误密码 | 200 后 running；403 后 error |
| 心跳 | 周期到达且 XML 字段正确；平台静默 N 次 → 观察到重注册 |
| 目录/信息/状态/配置/录像查询 | 应答 XML 字段（SN 回显、Item 内容、SumNum 等） |
| 控制指令矩阵 | PTZ OK、TeleBoot 默认 ERROR、IFrame 回调触发 |
| INVITE(UDP) 收流 | 200 的 answer SDP 字段正确；收包重组 PS 可解析：首帧 IDR+SPS/PPS、PTS 递增、Marker 规则、SSRC==y= |
| 音频打包（PS 内部） | PSM stream_type 正确（视频 0x1B/0x24、音频 0x0F）、PES 0xC0、AAC 帧带 7 字节 ADTS；音频 PES 与视频 AU 同 burst（marker 仅突发末包） |
| 音频时钟 | PES/PS 内音频 PTS 90kHz 单调、与视频 PTS 对齐（±一帧内） |
| INVITE(TCP 被动/主动) | 两种连接方向均收流成功（`$`-帧格式） |
| 重复 INVITE / BYE 幂等 | 无重复会话、无泄漏 |
| Stop | 平台收到 `Expires:0` 注销；订阅清理 |
| 媒体校验（需 ffmpeg） | 重组 PS 存盘 → `ffmpeg -i out.ps -f null -` 视频+音频均解码通过；音频 volumedetect 非静音（静音≈-91dB，参照既有方法） |

### 11.3 L3 集成（MediaHub 全链路）

`MP4Source → DefaultPipe → Gb28181Sink → FakePlatform`：验证帧完整到达（NALU 计数/关键帧序列）、源切换（重 Configure）、管道 Stop 级联、"无视频源/H265 源"的错误路径。

### 11.4 L4 实测（真实平台，手动/CI 可选）

部署 wvp-GB28181-pro（+ZLMediaKit，docker），验收清单：
- [ ] 设备出现在平台且在线；心跳持续 10 分钟无掉线
- [ ] 目录/设备信息/状态查询正确
- [ ] 预览出图（首帧 < 2s），播放 5 分钟无花屏；平台转 RTMP/HLS 可播
- [ ] PTZ 下发有应答；遥测/状态正常
- [ ] 平台重启 → 设备自动重注册；设备停止 → 平台转离线
- [ ] 传输矩阵：UDP、TCP 被动、TCP 主动 三种模式均出图

---

## 12. 里程碑

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **M1 骨架+注册** | 依赖/内置选型落地、配置模型/透传/校验、`Gb28181Sink` 骨架、注册/心跳/注销、FakePlatform 基础 | 单测绿；FakePlatform 上完成注册→心跳→注销全流程 |
| **M2 指令应答+接线** | 目录/信息/状态/配置/录像查询/控制/订阅/OPTIONS 应答；服务层与前端表单 | 前端可创建/启动/停止；指令矩阵测试全绿 |
| **M3 媒体推流** | INVITE→GOP 重放→PS（视频+音频）→RTP（UDP）；会话管理/背压/统计 | FakePlatform 收流可解码出图（含音频）；ffmpeg 校验通过 |
| **M4 健壮性+联调** | TCP 媒体（$-帧）、多会话、异常矩阵（§10）、优雅停止、日志/客户端展示；wvp 实机联调 | L4 清单通过；全量回归绿 |
| **M5（可选）** | 录像回放/下载、目录订阅 NOTIFY、SIPS/TLS、GB35114 国密 | 按决议单独立项 |

每个里程碑遵守仓库流程：**先加红测试 → 实现 → 单测/回归绿 → 实测（ffmpeg/平台）**，变更及时提交。

---

## 13. 风险与开放问题（请评审确认）

| # | 问题 | 选项/建议 |
|---|---|---|
| Q1 | **音频实现路径**（平台支持已确认：AAC 直封 PS、**无需转码**）。推荐库的 `device` 媒体路径仅 H264 视频且无扩展缝（`server.go:1567` 直调 `MuxH264ToPS`）；同库 `psmux` 已支持 AAC/G711 + H264/H265 | a) **A+：内置（vendor）后把媒体调用点切换为 psmux**（推荐，一期即含音频） b) A：官方依赖原样（视频-only），音频等上游合入/二期 c) B：完全自研 |
| Q2 | **H.265 是否纳入一期？** 平台侧与 PS 封装（PSM 0x24）均无碍（`psmux` 已支持），仅取决于媒体层路径 | 建议随 Q1-a 一起纳入（边际工作量很小，H264/H265 同一条路径）；否则二期 |
| Q3 | **依赖/内置形态**（MIT、依赖极轻） | a) A+：内置 `server/internal/gb28181/`（保留版权头，随上游 rebase/PR） b) A：外部依赖锁 v0.12.0 |
| Q4 | **go 1.26 升级**（go.mod + Dockerfile builder 镜像） | 建议随 M1 执行；dev 机已是 1.26.5 |
| Q5 | **单通道 vs 多通道**（一台设备挂多路流） | v1 单通道（虚拟 IPC）；多通道后续（每路一个 Output 实例即可近似满足） |
| Q6 | **联调目标平台** | 建议 wvp-GB28181-pro（开源、可控、ZLM 生态）；如需海康/大华平台请指定，联调时间另估 |
| Q7 | **TeleBoot（远程重启）语义** | 建议默认拒绝（记录），可配置开放为"重启 x-media 进程" |
| Q8 | **平台校验 MESSAGE 目标 URI 的 SIP ID**（库未暴露 platform_id 配置） | 库已在真实平台验证；若目标平台不兼容，M4 内适配（库可控点：RequestURI 构造） |
| Q9 | **安全性**：密码明文存库（同现有 RTSP 密码先例）；无 TLS | v1 维持现状；如需 SIPS 列 M5 |

---

## 附录 A：库适配要点备忘（实现参考）

- 信令：`device.New(cfg, DeviceInfo, FrameSource)` → `srv.Start(ctx)`（阻塞，放 goroutine）；`srv.SetMetricsHooks` / `SetControlHandlers` / `SetConfigHandlers` / `Deregister(ctx)` / `Stop()`；
- 订阅：`FrameSource.Subscribe(ctx)` 在 INVITE 时调用、ctx Done（BYE）时注销；`FrameSubscription{ID, <-chan AccessUnit}`；
- 媒体（A+ 接缝）：`device` 包内媒体调用点仅两处——`server.go:1567`（`MuxH264ToPS`→`pusher.SendFrame`）与 `playback.go:262`；替换为 `psmux.Muxer`（`WriteAU`/`WriteAudio`/`AppendAudioPES`）+ `psmux.RTPPacketizer`；`FrameSource` 需扩展音频通道（或 AccessUnit 内携带音频帧）；
- PS 复用（psmux）：`packHeader`（SCR=PTS/300）→ IDR 帧附 `systemHeader`+`psm`（CRC32/MPEG-2）→ `videoPES`（≤60000 字节切分，续包无 PTS）→ `AppendAudioPES`（音频并入同一突发）；
- RTP：负载 ≤1400、Marker 仅突发末包、TCP `$`-帧（`0x24|channel|len16`）、SSRC 取 INVITE `y=`、初始序号随机；
- XML：输出 `GB2312` 声明 + 正文 ASCII/UTF-8；入方向自动 UTF-8/GBK/GB18030；
- 版本要求：模块声明 `go 1.26`；`Config.Validate()` 启动即校验（拒绝坏配置）。

## 附录 B：参考资料

- GB/T 28181-2016 / 2022（信令、MANSCDP、媒体封装章节：附录 A/B/C，stream_type 表）
- wvp-GB28181-pro（平台侧参照）：`SIPCommander.java`（INVITE offer）、`InviteRequestProcessor.createSendSdp`（设备侧 answer）、`SIPCommanderForPlatform`（级联设备角色）
- ZLMediaKit：`src/Rtp/PSEncoder.cpp`、`src/Extension/CommonRtp.cpp`（PS→RTP 分包与 Marker 规则）；`src/Record/MPEG.cpp`（AAC 必须带 ADTS 头的强校验）；`src/Rtp/RtpSplitter.cpp`（TCP 媒体 `$`-帧解析实证）
- gb28181-go（设备侧库，含 conformance loopback 测试）：`device`（UAC 信令/媒体）、`psmux`（PS 复用：H264/H265/AAC/G711、PSM/CRC、音频并入突发等实测注释）——`https://github.com/mickeyzzc/gb28181-go`
- 本仓库：`docs/design.md` §12（媒体管道标准）、`docs/test-plan.md`（Mock 三角验证）、`docs/rtsp-aac-au-header.md`（Marker 位教训）
