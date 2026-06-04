# X-Media 媒体协议测试方案

## 测试环境

| 项目 | 值 |
|------|-----|
| 后端地址 | http://localhost:18090 |
| 前端地址 | http://localhost:18091 |
| ffmpeg 版本 | 6.1.1-3ubuntu5 |
| 测试文件 H264 | uploads/9116241a-2124-d4e3-bf68-e7eaf457446b.mp4 (852x480, AAC) |
| 测试文件 H265 | test_data/h265_test.mp4 (1280x720, 无音频) |

## 已有资源

| 类型 | 名称 | ID | 配置 | 状态 |
|------|------|----|------|------|
| 输入 | limu-teach | f8d05543-... | 文件 H264, loop=true | running |
| 输入 | test probe | 62936972-... | 文件 H265 | stopped |
| 输出 | rtsp1 | f67092c3-... | RTSP server :18001 | running |
| 管道 | - | 10cc1960-... | limu-teach → rtsp1 | running |

---

## 测试矩阵

| # | 输入类型 | 输出类型 | 验证方式 | 优先级 |
|---|---------|---------|---------|--------|
| T1 | 文件 (H264) | RTSP server | ffplay/ffprobe 拉流 | P0 |
| T2 | 文件 (H264) | RTMP | ffplay/ffprobe 拉流 | P0 |
| T3 | 文件 (H264) | HTTP-FLV | ffplay/curl 拉流 | P0 |
| T4 | 文件 (H265) | RTSP server | ffplay/ffprobe 拉流 | P1 |
| T5 | 文件 (H265) | RTMP | ffplay/ffprobe 拉流 | P1 |
| T6 | 文件 (H265) | HTTP-FLV | ffplay/curl 拉流 | P1 |
| T7 | RTSP 拉流 | RTSP server | 转发验证 | P2 |
| T8 | RTSP 拉流 | HTTP-FLV | 转发验证 | P2 |
| T9 | 文件 | 多输出 fan-out | 同时 RTMP + HTTP-FLV | P2 |
| T10 | 文件 | 多管道并发 | 2个文件→2个输出 | P3 |

---

## 测试步骤

### T1: 文件(H264) → RTSP server

**前置：** 系统已有 limu-teach 输入和 rtsp1 输出，管道已连接且 running。

**步骤：**

```bash
# 1. 验证 RTSP 端口已监听
ss -tlnp | grep ':18001'

# 2. 用 ffprobe 检测 RTSP 流
ffprobe -v quiet -rtsp_transport tcp -print_format json -show_streams rtsp://localhost:18001/live

# 3. 用 ffplay 播放 (有界面机器)
ffplay -rtsp_transport tcp rtsp://localhost:18001/live

# 4. 用 ffmpeg 录制 5 秒验证
ffmpeg -y -rtsp_transport tcp -i rtsp://localhost:18001/live -t 5 -c copy /tmp/t1_rtsp_out.mp4
ffprobe -v quiet -print_format json -show_streams /tmp/t1_rtsp_out.mp4
```

**预期：**
- ffprobe 能获取到 H264 视频流信息
- ffplay 能播放出画面
- 录制文件能正常播放，codec 为 h264

---

### T2: 文件(H264) → RTMP

**步骤：**

```bash
# 1. 创建 RTMP 输出 (通过 API)
curl -s -X POST http://localhost:18090/api/v1/outputs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "rtmp-test",
    "type": "rtmp",
    "config": "{\"url\":\"rtmp://localhost:1935/live/t2\"}"
  }'

# 2. 创建管道
curl -s -X POST http://localhost:18090/api/v1/pipes \
  -H "Content-Type: application/json" \
  -d '{"input_id":"f8d05543-b506-d93d-21b9-0a70651d1935","output_id":"<rtmp-test-id>"}'

# 3. 启动管道
curl -s -X POST http://localhost:18090/api/v1/pipes/<pipe-id>/start

# 4. 验证 ffmpeg 进程存在
ps aux | grep "ffmpeg.*rtmp"

# 5. 用 ffprobe 检测 RTMP 流
ffprobe -v quiet -print_format json -show_streams rtmp://localhost:1935/live/t2

# 6. 用 ffmpeg 录制 5 秒
ffmpeg -y -i rtmp://localhost:1935/live/t2 -t 5 -c copy /tmp/t2_rtmp_out.flv
ffprobe -v quiet -print_format json -show_streams /tmp/t2_rtmp_out.flv
```

**预期：**
- ffmpeg 子进程运行中
- ffprobe 能获取到流信息
- 录制文件能播放

**说明：** RTMP 输出需要外部 RTMP 服务器接收。如果本地没有 nginx-rtmp，此测试仅验证 ffmpeg 进程是否启动。

---

### T3: 文件(H264) → HTTP-FLV

**步骤：**

```bash
# 1. 创建 HTTP-FLV 输出
curl -s -X POST http://localhost:18090/api/v1/outputs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "httpflv-test",
    "type": "http-flv",
    "config": "{\"addr\":\":18002\"}"
  }'

# 2. 创建管道并启动
curl -s -X POST http://localhost:18090/api/v1/pipes \
  -H "Content-Type: application/json" \
  -d '{"input_id":"f8d05543-b506-d93d-21b9-0a70651d1935","output_id":"<httpflv-test-id>"}'

curl -s -X POST http://localhost:18090/api/v1/pipes/<pipe-id>/start

# 3. 等待 2 秒
sleep 2

# 4. 用 curl 检测 HTTP-FLV 端点
curl -s --max-time 5 -o /dev/null -w "%{http_code}" http://localhost:18002/<httpflv-test-id>.flv

# 5. 用 curl 检测 /live/ 路径
curl -s --max-time 5 -o /dev/null -w "%{http_code}" http://localhost:18002/live/<httpflv-test-id>.flv

# 6. 用 ffplay 播放
ffplay http://localhost:18002/<httpflv-test-id>.flv

# 7. 用 ffmpeg 录制 5 秒
ffmpeg -y -i http://localhost:18002/<httpflv-test-id>.flv -t 5 -c copy /tmp/t3_flv_out.flv
ffprobe -v quiet -print_format json -show_streams /tmp/t3_flv_out.flv
```

**预期：**
- HTTP 端点返回 200
- Content-Type 为 video/x-flv
- ffplay 能播放
- 录制文件是有效 FLV

---

### T4: 文件(H265) → RTSP server

**步骤：**

```bash
# 1. 停止现有管道和输出 (如果需要新建)
# 2. 创建新输入使用 H265 文件
curl -s -X POST http://localhost:18090/api/v1/inputs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "h265-input",
    "type": "file",
    "config": "{\"path\":\"test_data/h265_test.mp4\",\"loop\":true}"
  }'

# 3. 创建 RTSP 输出 (端口 18003)
curl -s -X POST http://localhost:18090/api/v1/outputs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "rtsp-h265",
    "type": "rtsp",
    "config": "{\"mode\":\"server\",\"addr\":\":18003\",\"transport\":\"tcp\"}"
  }'

# 4. 创建管道并启动
# 5. 验证
ffprobe -v quiet -rtsp_transport tcp -print_format json -show_streams rtsp://localhost:18003/live
```

**预期：**
- ffprobe 显示 hevc codec
- 分辨率 1280x720

---

### T5: 文件(H265) → RTMP

步骤同 T2，使用 H265 输入。

---

### T6: 文件(H265) → HTTP-FLV

步骤同 T3，使用 H265 输入。

---

### T7: RTSP 拉流 → RTSP server

**步骤：**

```bash
# 1. 用 ffmpeg 启动一个临时 RTSP 源
ffmpeg -re -i test_data/h265_test.mp4 -c copy -f rtsp rtsp://localhost:18004/source &
FFMPEG_PID=$!

# 2. 创建 RTSP 拉流输入
curl -s -X POST http://localhost:18090/api/v1/inputs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "rtsp-pull",
    "type": "rtsp",
    "config": "{\"url\":\"rtsp://localhost:18004/source\",\"transport\":\"tcp\"}"
  }'

# 3. 创建 RTSP 输出 (18005)
# 4. 创建管道并启动
# 5. 验证
ffprobe -v quiet -rtsp_transport tcp -print_format json -show_streams rtsp://localhost:18005/live

# 6. 清理
kill $FFMPEG_PID
```

**预期：**
- 拉流输入正常获取数据
- 转发到 RTSP 输出端，可播放

---

### T8: RTSP 拉流 → HTTP-FLV

步骤同 T7，输出改为 HTTP-FLV。

---

### T9: 文件 → 多输出 fan-out

**步骤：**

```bash
# 1. 创建 2 个输出 (RTMP + HTTP-FLV)
# 2. 创建 2 条管道，同一个输入连接到 2 个输出
# 3. 启动两条管道
# 4. 同时验证两个输出端都有数据
```

**预期：**
- 两个输出端同时收到数据
- 不互相影响

---

### T10: 多管道并发

**步骤：**

```bash
# 1. 创建 2 个文件输入 (H264 + H265)
# 2. 创建 2 个 HTTP-FLV 输出 (不同端口)
# 3. 创建 2 条管道
# 4. 同时验证
```

---

## 清理命令

```bash
# 停止所有管道
for pid in $(curl -s http://localhost:18090/api/v1/pipes | jq -r '.data[].id'); do
  curl -s -X POST http://localhost:18090/api/v1/pipes/$pid/stop
  curl -s -X DELETE http://localhost:18090/api/v1/pipes/$pid
done

# 停止所有输出
for oid in $(curl -s http://localhost:18090/api/v1/outputs | jq -r '.data[].id'); do
  curl -s -X POST http://localhost:18090/api/v1/outputs/$oid/stop
  curl -s -X DELETE http://localhost:18090/api/v1/outputs/$oid
done

# 停止所有输入 (非系统默认的)
for iid in $(curl -s http://localhost:18090/api/v1/inputs | jq -r '.data[].id'); do
  curl -s -X POST http://localhost:18090/api/v1/inputs/$iid/stop
done
```

## 已知限制

1. RTMP 输出需要外部 RTMP 服务器 (如 nginx-rtmp-module) 才能验证完整链路
2. RTSP 输出当前使用 ffmpeg 子进程推流，server 模式需要 ffmpeg 内置 RTSP server 支持
3. H265 文件无音频流，仅验证视频
4. 当前 ffmpeg 输出格式固定为 h264，H265 文件需要调整 ffmpeg 参数

## 待确认

1. 是否需要搭建本地 RTMP 服务器 (nginx-rtmp) 用于 T2/T5 测试？
2. H265 输出是否需要支持（当前 ffmpeg 参数写死 h264）？
3. 测试优先级是否合理？P0 是否全部执行？
