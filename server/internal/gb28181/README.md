# internal/gb28181 —— 内置的 GB28181 设备端代码（vendor）

本目录是 [mickeyzzc/gb28181-go](https://github.com/mickeyzzc/gb28181-go) 的**内置副本（vendor）**，
用于实现 x-media 的 GB28181 Sink（虚拟摄像头）。来源与许可：

- 上游版本：`v0.12.0`（Vendored: 2026-10-15）
- 许可：MIT（见本目录 `LICENSE`，版权归 Mi-Bee Studio contributors）
- 内置范围（仅非测试文件）：
  - `device/` —— UAC 信令与媒体（REGISTER/摘要认证、MANSCDP、INVITE/PS-RTP、心跳/重注册）
  - `manscdp/` —— 国标 XML 编解码（UTF-8/GBK/GB18030 自动识别）
  - `metrics/` —— 观测钩子接口
  - `psmux/` —— PS 复用（H.264/H.265 + AAC/G.711、PSM/CRC32、RTP 打包）
  - 未内置：`platform/`、`security35114/`（上游仅测试使用）、上游测试文件

## 本地补丁（x-media 变更，均以注释标注 "Local patch"）

| 文件 | 补丁 | 对应方案 |
|---|---|---|
| `device/seam.go`、`device/validate.go`、`device/server.go` | 新增 `Config.PlatformID`：出站请求 URI（REGISTER/注销/心跳）与心跳 To 头使用 `sip:<platform_id>@<domain>`；未配置时保持上游行为 | §5.9 平台兼容性 |
| `device/server.go`、`device/manscdp.go` | 已执行的控制指令补充显式 `Result=OK` 应答（上游仅回 SIP 200；拒绝路径保留 ERROR 应答） | §5.4 指令应答 |
| `device/manscdp.go` | 目录通道 `Name` 使用设备展示名（未配置时回退通道编码），与方案 §5.3 目录示例一致 | §5.3 目录应答 |
| `device/manscdp.go`、`device/infoquery.go` | **出站 MANSCDP 切换为元素形式**（`<CmdType>Catalog</CmdType>`；科达 KDM 解析器只认元素，属性形式判"未能解析"，wvp 类同样读元素）——目录/设备信息/状态/配置/控制应答/录像/2022 查询/Keepalive 等全部出站构造器 | §5.2/§5.9 |
| `device/seam.go` | `AccessUnit` 扩展 `Audio []AudioFrame`（音频随行同一 PS 突发）；`Config.VideoCodec`/`AudioCodec`（h264/h265、aac/g711a/g711u） | §5.7 帧适配 |
| `device/server.go`、`device/ps.go` | 直播媒体路径切换为 `psmux`（替换 H264-only 的 `MuxH264ToPS`）：H264/H265 + AAC/G711；新增 `Server.SetMediaCodecs`（管道协商晚于 Start）；`annexBFromNALUs` 重组助手 | §5.6/§5.7 媒体封装 |
| `device/server.go`、`device/rtp_pusher.go` | `Config.TCPRole` 强制 TCP 媒体角色（异常平台 setup 语义相反）；`Config.RTPMTU` + `RtpPusher.SetMTU` + `Server.SetMediaMTU` | §5.9 平台兼容性 |
| `device/server.go`、`device/seam.go`、`device/validate.go` | 媒体会话元数据 `LiveSession()`（远端/传输/SSRC/开始时间），供输出端"当前客户端"展示；`TCPRole`/`RTPMTU` 校验 | §9 可观测性 |
| `device/server.go` | **NAT 穿越**：响应 Via `received/rport` 学习外部地址 → Contact 使用（认证腿起生效）、`Config.LocalIP` 显式覆盖、`LocalSIPPort=0` 绑定后回填真实端口；404 响应不触发重注册；REGISTER Call-ID 用纳秒（避免 482 Loop Detected） | §5.9 NAT |
| `device/server.go`、`device/seam.go`、`device/validate.go` | **TCP 媒体发送端匹配**（科达 KDM 实测）：出站 TCP 媒体先选本地端口（`Config.MediaTCPPort`，0=自动）→ 应答 SDP `c=` 用 NAT 学习到的外部 IP、`m=` 用该端口 → 200 OK 后从该端口拨出（平台按 `c=/m=` 登记预期发送端、连接到达时按源 ip:port 匹配） | §5.9 NAT/KDM |
| 未修改（保留上游） | 回放路径 `playback.go`（仍用 `MuxH264ToPS`，M5 范围）；`platform`/`security35114` 未内置 | — |

维护约定：

- **不要在本目录做与上游无关的重构**；补丁保持小而集中，便于向上游回提（PR）与跟随 rebase；
- 上游升级流程：整体替换 `device/manscdp/metrics/psmux` → 重新应用本表补丁 → `go build ./...` + 全量测试；
- x-media 侧所有引用集中在 `internal/media/gb28181_sink.go`（及其 hub），其余业务代码不直接依赖本目录。
