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
| 后续（M3/M4） | 媒体层切换 psmux（音频/H265）；`tcp_role`/`rtp_mtu` 开关 | §5.4/§5.9 |

维护约定：

- **不要在本目录做与上游无关的重构**；补丁保持小而集中，便于向上游回提（PR）与跟随 rebase；
- 上游升级流程：整体替换 `device/manscdp/metrics/psmux` → 重新应用本表补丁 → `go build ./...` + 全量测试；
- x-media 侧所有引用集中在 `internal/media/gb28181_sink.go`（及其 hub），其余业务代码不直接依赖本目录。
