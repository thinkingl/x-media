# GB28181 联调工具（双实现互操作 · L4）

用 [gb28181-go](https://github.com/mickeyzzc/gb28181-go) 的**平台端**（MiBeeNvr 生产实现）
作为"真实平台"，联调 x-media 的 GB28181 Sink（方案 `docs/gb28181-sink-design.md` §11.4）。

## 用法

1. 启动平台端（本目录为独立 Go module）：

   ```bash
   cd server/test/interop/gb28181-platform
   go run .
   # PLATFORM listening :25060 serverID=34020000002000000001
   ```

   默认参数：SIP `:25060`、serverID `34020000002000000001`、域 `3402000000`、
   密码 `interop1234`、RTP 端口池 `30500-30550`（可用环境变量覆盖，见下）。

2. 在 x-media 创建 GB28181 输出端（Web 界面或 API），指向该平台：

   ```json
   {
     "platform_ip": "127.0.0.1", "platform_port": 25060,
     "platform_id": "34020000002000000001",
     "device_id": "34020000001320000001",
     "channel_id": "34020000001310000001",
     "sip_domain": "3402000000", "password": "interop1234"
   }
   ```

3. 建立并启动管道（任意输入，如 `test/fixtures/test.mp4` 文件源）。

4. 平台端自动完成：digest 注册 → 目录查询 → 设备信息/状态 → INVITE 点播 →
   PS over RTP 收流统计 → BYE；成功时输出 `SUMMARY {..., "ok": true}` 且退出码 0。

## 断言口径（SUMMARY 字段）

| 字段 | 含义 |
|---|---|
| `registered` | 摘要认证注册成功 |
| `catalog` | 目录收到通道数（≥1） |
| `videoAUs` / `idrs` | 平台端解出的视频 AU 数（≥30）与 IDR 数（≥1） |
| `audioFrames` | 平台端解出的音频帧数（≥10） |
| `audioCodec` / `audioConfig` | 音频编码（aac）与平台从 ADTS 还源的 ASC（应与源一致） |

## 环境变量

`DEVICE_ID`、`CHANNEL_ID`、`SIP_LISTEN`、`SERVER_ID`、`REALM`、`PASSWORD`、
`PORT_RANGE`（如 `30500-30550`）、`RUN_SECONDS`（默认 12）。
