# X-Media 媒体流处理平台设计方案

## 1. 项目概述

X-Media 是一个前后端分离的媒体流处理平台，支持多种媒体协议的输入输出，可以在输入输出端之间建立媒体流管道连接，并提供图形化的数据流监控和统计功能。

## 2. 技术选型

### 2.1 前端技术栈

**推荐：Vue3 + Vite + Element Plus**

| 方案 | 成熟度 | AI支持度 | 生态 | 推荐度 |
|------|--------|----------|------|--------|
| Vue3 + Vite + Element Plus | 高 | 优秀 (GitHub Copilot, Cursor) | 丰富 | ⭐⭐⭐⭐⭐ |
| React + Next.js + Ant Design | 高 | 优秀 | 丰富 | ⭐⭐⭐⭐ |

**选择 Vue3 的理由：**
- 学习曲线平缓，文档完善
- 中文社区活跃，资料丰富
- AI 工具支持度好（Copilot、Cursor 等）
- Element Plus 组件库成熟，适合后台管理系统
- Vite 构建速度快

### 2.2 后端技术栈

- **语言**: Go 1.21+
- **Web框架**: Gin
- **ORM**: GORM
- **数据库**: SQLite
- **WebSocket**: gorilla/websocket
- **日志**: zap
- **配置**: viper

### 2.3 流媒体中间件方案对比

| 方案 | 协议支持 | Go集成 | 性能 | 复杂度 | 社区 | 推荐场景 |
|------|----------|--------|------|--------|------|----------|
| **LAL** | RTMP, RTSP, HLS, HTTP-FLV/TS, GB28181 | 原生Go库 | 高 | 低 | 中文社区 | 直播场景，快速开发 |
| **FFmpeg** | 所有协议 | CGO或进程调用 | 最高 | 中 | 全球社区 | 需要转码，功能全面 |
| **GStreamer** | 所有协议 | CGO | 高 | 高 | 全球社区 | 复杂媒体处理 |
| **自研** | 按需实现 | 原生 | 可控 | 最高 | 无 | 特殊需求 |

**推荐方案：LAL + FFmpeg 组合**

1. **LAL 作为核心**：
   - 纯 Go 实现，无 CGO 依赖
   - 支持主流直播协议
   - 代码清晰，易于扩展
   - 可作为 Go 库直接调用

2. **FFmpeg 作为补充**：
   - 后期需要转码时引入
   - 通过进程调用，避免 CGO 复杂性
   - 支持几乎所有媒体格式

3. **架构示意**：
   ```
   ┌─────────────────────────────────────────────────────────┐
   │                      X-Media Platform                    │
   ├─────────────────────────────────────────────────────────┤
   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
   │  │   Input      │  │   Router    │  │   Output     │     │
   │  │   Manager    │  │   (Pipe)    │  │   Manager    │     │
   │  └──────┬───────┘  └──────┬──────┘  └──────┬───────┘     │
   │         │                 │                 │             │
   │  ┌──────▼─────────────────▼─────────────────▼───────┐   │
   │  │              Media Engine (LAL)                   │   │
   │  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ │   │
   │  │  │  RTMP   │ │  RTSP   │ │  HLS    │ │ HTTP-FLV│ │   │
   │  │  └─────────┘ └─────────┘ └─────────┘ └─────────┘ │   │
   │  └───────────────────────────────────────────────────┘   │
   │                          │                               │
   │  ┌───────────────────────▼───────────────────────────┐   │
   │  │              FFmpeg (后期引入)                      │   │
   │  │         转码 / 格式转换 / 特殊处理                  │   │
   │  └───────────────────────────────────────────────────┘   │
   └─────────────────────────────────────────────────────────┘
   ```

## 3. 系统架构

### 3.1 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                        前端 (Vue3)                          │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐          │
│  │ 输入管理 │ │ 输出管理 │ │ 流程监控 │ │ 日志管理 │          │
│  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘          │
│       │           │           │           │                 │
│       └───────────┴───────────┴───────────┘                 │
│                           │                                 │
│                    RESTful API + WebSocket                  │
└───────────────────────────┬─────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────┐
│                      后端 (Go)                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                   API Gateway (Gin)                  │   │
│  └─────────────────────────────────────────────────────┘   │
│                           │                                 │
│  ┌───────────┬───────────┼───────────┬───────────┐         │
│  │           │           │           │           │         │
│  ▼           ▼           ▼           ▼           ▼         │
│ ┌─────┐   ┌─────┐   ┌─────┐   ┌─────┐   ┌─────┐         │
│ │Input│   │Output│  │ Pipe │   │Stats│   │ Log │         │
│ │Svc  │   │ Svc  │  │ Svc  │   │ Svc │   │ Svc │         │
│ └─────┘   └─────┘   └─────┘   └─────┘   └─────┘         │
│                           │                                 │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              Media Engine (LAL)                      │   │
│  └─────────────────────────────────────────────────────┘   │
│                           │                                 │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              Storage (SQLite + GORM)                 │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 核心模块

#### 3.2.1 输入管理器 (Input Manager)
- 管理各种协议的输入端
- 支持的输入类型：
  - 本地文件：MP4, FLV, TS 等
  - 网络流：RTMP, RTSP, HLS, HTTP-FLV 等
- 输入端生命周期管理（创建、启动、停止、删除）

#### 3.2.2 输出管理器 (Output Manager)
- 管理各种协议的输出端
- 支持的输出类型：
  - RTMP 推流
  - RTSP 推流/服务
  - HLS 切片
  - HTTP-FLV/TS 服务
  - 录制文件

#### 3.2.3 管道服务 (Pipe Service)
- 在输入输出端之间建立连接
- 支持一对多、多对一映射
- 流状态监控和错误恢复

#### 3.2.4 统计服务 (Stats Service)
- 实时流量统计
- 连接数监控
- 带宽使用情况
- 帧率、码率统计

#### 3.2.5 日志服务 (Log Service)
- 结构化日志输出
- 日志级别控制
- 日志轮转配置
- WebSocket 实时推送

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

## 6. 项目结构

```
x-media/
├── docs/                          # 文档
│   └── design.md
├── web/                           # 前端项目
│   ├── src/
│   │   ├── api/                   # API接口
│   │   ├── components/            # 组件
│   │   ├── views/                 # 页面
│   │   ├── stores/                # 状态管理
│   │   ├── utils/                 # 工具函数
│   │   └── App.vue
│   ├── package.json
│   └── vite.config.js
├── server/                        # 后端项目
│   ├── cmd/                       # 入口
│   │   └── main.go
│   ├── internal/                  # 内部包
│   │   ├── api/                   # API处理器
│   │   ├── service/               # 业务逻辑
│   │   ├── repository/            # 数据访问
│   │   ├── model/                 # 数据模型
│   │   ├── media/                 # 媒体引擎
│   │   └── config/                # 配置
│   ├── pkg/                       # 公共包
│   │   ├── logger/                # 日志
│   │   ├── errors/                # 错误处理
│   │   └── utils/                 # 工具函数
│   ├── go.mod
│   └── go.sum
├── scripts/                       # 脚本
├── docker/                        # Docker配置
├── Makefile
└── README.md
```

## 7. 第一步实现计划 (MP4 + RTSP)

### 7.1 功能范围

1. **输入端支持**：
   - 本地 MP4 文件读取
   - RTSP 流拉取

2. **输出端支持**：
   - RTMP 推流
   - RTSP 推流/服务
   - HTTP-FLV 服务

3. **管道功能**：
   - 输入到输出的流转发
   - 基本状态管理

4. **前端功能**：
   - 输入端管理页面
   - 输出端管理页面
   - 管道管理页面
   - 实时状态监控

### 7.2 开发步骤

1. **Phase 1: 基础框架** (1周)
   - 搭建前后端项目框架
   - 数据库设计和初始化
   - 基础API实现

2. **Phase 2: 媒体引擎** (2周)
   - 集成LAL
   - 实现MP4文件输入
   - 实现RTSP输入
   - 实现RTMP输出
   - 实现RTSP输出
   - 实现HTTP-FLV输出

3. **Phase 3: 管道服务** (1周)
   - 实现输入输出连接
   - 状态管理和错误恢复
   - 基本统计功能

4. **Phase 4: 前端开发** (2周)
   - 输入端管理界面
   - 输出端管理界面
   - 管道配置界面
   - 实时监控界面

5. **Phase 5: 完善功能** (1周)
   - 日志系统完善
   - 统计功能完善
   - 单元测试
   - 文档完善

## 8. 非功能需求

### 8.1 性能要求
- 支持并发处理多个媒体流
- 单机支持至少50个并发流
- 延迟控制在秒级

### 8.2 可靠性
- 流中断自动重连
- 异常状态自动恢复
- 配置持久化，重启自动恢复

### 8.3 可扩展性
- 协议插件化，便于添加新协议
- 模块解耦，便于功能扩展

### 8.4 日志规范
- 结构化日志 (JSON格式)
- 日志级别：DEBUG, INFO, WARN, ERROR
- 支持日志轮转：按大小、按时间
- 前端可配置日志级别和轮转策略

## 9. 后续扩展

### 9.1 协议扩展
- HLS 输入/输出
- GB28181 输入
- WebRTC 输入/输出
- SRT 输入/输出

### 9.2 功能扩展
- 转码功能
- 录制功能
- 集群支持
- 负载均衡

---

## 10. Docker 部署

### 10.1 Dockerfile

```dockerfile
# 后端
FROM golang:1.21-alpine AS backend-builder
WORKDIR /build
COPY server/ .
RUN go mod download && CGO_ENABLED=1 GOOS=linux go build -o x-media-server ./cmd/main.go

# 前端
FROM node:18-alpine AS frontend-builder
WORKDIR /build
COPY web/ .
RUN npm install && npm run build

# 运行镜像
FROM alpine:3.18
RUN apk add --no-cache sqlite-libs ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /build/x-media-server .
COPY --from=frontend-builder /build/dist ./web/dist
COPY docker/config.yaml ./config.yaml
EXPOSE 8080 1935 5544
CMD ["./x-media-server", "-c", "config.yaml"]
```

### 10.2 docker-compose.yml

```yaml
version: "3.8"
services:
  x-media:
    build: .
    container_name: x-media
    ports:
      - "8080:8080"    # Web API + 前端
      - "1935:1935"    # RTMP
      - "5544:5544"    # RTSP
      - "8080:8080/tcp" # HTTP-FLV
    volumes:
      - ./data:/app/data        # 数据库
      - ./logs:/app/logs        # 日志
      - ./videos:/app/videos    # 媒体文件
    restart: unless-stopped
```

### 10.3 配置文件 (config.yaml)

```yaml
server:
  http_addr: ":8080"
  rtmp_addr: ":1935"
  rtsp_addr: ":5544"

database:
  driver: "sqlite"
  dsn: "./data/x-media.db"

log:
  level: "info"
  filename: "./logs/x-media.log"
  max_size: 100
  max_backups: 5
  max_age: 30
```

### 10.4 启动命令

```bash
# 构建并启动
docker-compose up -d

# 查看日志
docker-compose logs -f

# 停止服务
docker-compose down
```

## 11. 测试方案

### 11.1 测试目标

- 单元测试覆盖率 >= 80%
- 核心模块（媒体引擎、管道服务）覆盖率 >= 90%
- 所有协议通过功能测试
- 关键路径通过集成测试

### 11.2 后端单元测试

#### 11.2.1 测试框架

| 工具 | 用途 |
|------|------|
| testing | Go 标准测试库 |
| testify | 断言和 mock 库 |
| goconvey | BDD 风格测试 |
| gomock | 接口 mock |
| sqlmock | 数据库 mock |

#### 11.2.2 测试目录结构

```
server/
├── internal/
│   ├── api/
│   │   ├── input_handler.go
│   │   └── input_handler_test.go      # API 测试
│   ├── service/
│   │   ├── input_service.go
│   │   └── input_service_test.go      # 业务逻辑测试
│   ├── repository/
│   │   ├── input_repo.go
│   │   └── input_repo_test.go         # 数据访问测试
│   └── media/
│       ├── mp4_reader.go
│       ├── mp4_reader_test.go         # 媒体模块测试
│       ├── rtsp_input.go
│       └── rtsp_input_test.go
├── pkg/
│   ├── logger/
│   │   ├── logger.go
│   │   └── logger_test.go
│   └── utils/
│       ├── utils.go
│       └── utils_test.go
└── test/
    ├── fixtures/                      # 测试数据
    │   ├── test.mp4
    │   └── config.json
    ├── mocks/                         # Mock 对象
    │   ├── mock_media_engine.go
    │   └── mock_repository.go
    └── helpers/                       # 测试辅助函数
        └── test_helper.go
```

#### 11.2.3 单元测试示例

```go
// server/internal/service/input_service_test.go
package service

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

// Mock 输入端仓储
type MockInputRepo struct {
    mock.Mock
}

func (m *MockInputRepo) Create(input *model.Input) error {
    args := m.Called(input)
    return args.Error(0)
}

func (m *MockInputRepo) GetByID(id string) (*model.Input, error) {
    args := m.Called(id)
    return args.Get(0).(*model.Input), args.Error(1)
}

// 测试用例
func TestInputService_Create(t *testing.T) {
    t.Run("成功创建MP4输入端", func(t *testing.T) {
        // Arrange
        mockRepo := new(MockInputRepo)
        svc := NewInputService(mockRepo)
        
        req := &CreateInputRequest{
            Name: "测试MP4",
            Type: "file",
            Config: `{"path":"/data/test.mp4","loop":true}`,
        }
        
        mockRepo.On("Create", mock.AnythingOfType("*model.Input")).Return(nil)
        
        // Act
        result, err := svc.Create(req)
        
        // Assert
        assert.NoError(t, err)
        assert.NotNil(t, result)
        assert.Equal(t, "测试MP4", result.Name)
        assert.Equal(t, "file", result.Type)
        mockRepo.AssertExpectations(t)
    })
    
    t.Run("参数验证失败-缺少名称", func(t *testing.T) {
        // Arrange
        mockRepo := new(MockInputRepo)
        svc := NewInputService(mockRepo)
        
        req := &CreateInputRequest{
            Type: "file",
            Config: `{"path":"/data/test.mp4"}`,
        }
        
        // Act
        result, err := svc.Create(req)
        
        // Assert
        assert.Error(t, err)
        assert.Nil(t, result)
        assert.Contains(t, err.Error(), "name")
    })
}

func TestInputService_GetByID(t *testing.T) {
    t.Run("成功获取输入端", func(t *testing.T) {
        // Arrange
        mockRepo := new(MockInputRepo)
        svc := NewInputService(mockRepo)
        
        expected := &model.Input{
            ID:   "input_001",
            Name: "测试MP4",
            Type: "file",
        }
        
        mockRepo.On("GetByID", "input_001").Return(expected, nil)
        
        // Act
        result, err := svc.GetByID("input_001")
        
        // Assert
        assert.NoError(t, err)
        assert.Equal(t, expected, result)
    })
}
```

#### 11.2.4 媒体模块测试

```go
// server/internal/media/mp4_reader_test.go
package media

import (
    "testing"
    "os"
    "github.com/stretchr/testify/assert"
)

func TestMP4Reader_Open(t *testing.T) {
    t.Run("成功打开MP4文件", func(t *testing.T) {
        // 准备测试文件
        testFile := "../../test/fixtures/test.mp4"
        if _, err := os.Stat(testFile); os.IsNotExist(err) {
            t.Skip("测试文件不存在")
        }
        
        reader := NewMP4Reader()
        err := reader.Open(testFile)
        
        assert.NoError(t, err)
        assert.True(t, reader.IsOpen())
        
        reader.Close()
    })
    
    t.Run("文件不存在", func(t *testing.T) {
        reader := NewMP4Reader()
        err := reader.Open("/nonexistent/file.mp4")
        
        assert.Error(t, err)
        assert.False(t, reader.IsOpen())
    })
}

func TestMP4Reader_ReadPacket(t *testing.T) {
    t.Run("读取音视频包", func(t *testing.T) {
        testFile := "../../test/fixtures/test.mp4"
        if _, err := os.Stat(testFile); os.IsNotExist(err) {
            t.Skip("测试文件不存在")
        }
        
        reader := NewMP4Reader()
        err := reader.Open(testFile)
        assert.NoError(t, err)
        defer reader.Close()
        
        // 读取几个包
        for i := 0; i < 10; i++ {
            pkt, err := reader.ReadPacket()
            assert.NoError(t, err)
            assert.NotNil(t, pkt)
            assert.True(t, pkt.IsVideo() || pkt.IsAudio())
        }
    })
}

func TestMP4Reader_Loop(t *testing.T) {
    t.Run("循环播放", func(t *testing.T) {
        testFile := "../../test/fixtures/test.mp4"
        if _, err := os.Stat(testFile); os.IsNotExist(err) {
            t.Skip("测试文件不存在")
        }
        
        reader := NewMP4Reader(WithLoop(true))
        err := reader.Open(testFile)
        assert.NoError(t, err)
        defer reader.Close()
        
        // 读取超过文件长度的数据，验证循环
        packetCount := 0
        for i := 0; i < 1000; i++ {
            pkt, err := reader.ReadPacket()
            if err != nil {
                break
            }
            packetCount++
            _ = pkt
        }
        
        assert.Greater(t, packetCount, 100)
    })
}
```

#### 11.2.5 数据库测试

```go
// server/internal/repository/input_repo_test.go
package repository

import (
    "testing"
    "github.com/DATA-DOG/go-sqlmock"
    "github.com/stretchr/testify/assert"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
    db, mock, err := sqlmock.New()
    assert.NoError(t, err)
    
    gormDB, err := gorm.Open(sqlite.Dialector{Conn: db}, &gorm.Config{})
    assert.NoError(t, err)
    
    return gormDB, mock
}

func TestInputRepo_Create(t *testing.T) {
    t.Run("成功创建", func(t *testing.T) {
        // Arrange
        db, mock := setupTestDB(t)
        repo := NewInputRepo(db)
        
        input := &model.Input{
            ID:     "input_001",
            Name:   "测试",
            Type:   "file",
            Config: `{"path":"test.mp4"}`,
        }
        
        mock.ExpectBegin()
        mock.ExpectExec("INSERT INTO inputs").
            WithArgs(input.ID, input.Name, input.Type, input.Config, sqlmock.AnyArg(), sqlmock.AnyArg()).
            WillReturnResult(sqlmock.NewResult(1, 1))
        mock.ExpectCommit()
        
        // Act
        err := repo.Create(input)
        
        // Assert
        assert.NoError(t, err)
        assert.NoError(t, mock.ExpectationsWereMet())
    })
}
```

### 11.3 前端单元测试

#### 11.3.1 测试框架

| 工具 | 用途 |
|------|------|
| Vitest | 测试运行器 |
| Vue Test Utils | Vue 组件测试 |
| Mock Service Worker | API Mock |
| Cypress | E2E 测试 |

#### 11.3.2 测试目录结构

```
web/
├── src/
│   ├── components/
│   │   ├── InputList.vue
│   │   └── InputList.test.ts        # 组件测试
│   ├── views/
│   │   ├── InputManagement.vue
│   │   └── InputManagement.test.ts   # 页面测试
│   ├── stores/
│   │   ├── inputStore.ts
│   │   └── inputStore.test.ts        # 状态管理测试
│   ├── api/
│   │   ├── inputApi.ts
│   │   └── inputApi.test.ts          # API 测试
│   └── utils/
│       ├── format.ts
│       └── format.test.ts            # 工具函数测试
├── tests/
│   ├── unit/                         # 单元测试
│   ├── integration/                  # 集成测试
│   └── e2e/                          # E2E 测试
│       └── input.cy.ts
└── vitest.config.ts
```

#### 11.3.3 组件测试示例

```typescript
// web/src/components/InputList.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import InputList from './InputList.vue'
import { useInputStore } from '@/stores/inputStore'

describe('InputList', () => {
  let wrapper: any
  let store: any

  beforeEach(() => {
    wrapper = mount(InputList, {
      global: {
        plugins: [
          createTestingPinia({
            createSpy: vi.fn,
            initialState: {
              input: {
                inputs: [
                  { id: '1', name: 'MP4输入', type: 'file', status: 'running' },
                  { id: '2', name: 'RTSP输入', type: 'rtsp', status: 'stopped' },
                ],
                loading: false,
              },
            },
          }),
        ],
      },
    })
    store = useInputStore()
  })

  it('渲染输入端列表', () => {
    const items = wrapper.findAll('.input-item')
    expect(items).toHaveLength(2)
  })

  it('显示输入端名称', () => {
    const firstItem = wrapper.find('.input-item:first-child .name')
    expect(firstItem.text()).toBe('MP4输入')
  })

  it('显示正确的状态标签', () => {
    const statusTags = wrapper.findAll('.status-tag')
    expect(statusTags[0].classes()).toContain('running')
    expect(statusTags[1].classes()).toContain('stopped')
  })

  it('点击删除按钮触发事件', async () => {
    const deleteBtn = wrapper.find('.input-item:first-child .delete-btn')
    await deleteBtn.trigger('click')
    
    expect(wrapper.emitted('delete')).toBeTruthy()
    expect(wrapper.emitted('delete')[0]).toEqual(['1'])
  })

  it('点击启动按钮调用store方法', async () => {
    const startBtn = wrapper.find('.input-item:nth-child(2) .start-btn')
    await startBtn.trigger('click')
    
    expect(store.startInput).toHaveBeenCalledWith('2')
  })
})
```

#### 11.3.4 Store 测试示例

```typescript
// web/src/stores/inputStore.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useInputStore } from './inputStore'
import * as inputApi from '@/api/inputApi'

vi.mock('@/api/inputApi')

describe('InputStore', () => {
  let store: any

  beforeEach(() => {
    setActivePinia(createPinia())
    store = useInputStore()
  })

  describe('fetchInputs', () => {
    it('成功获取输入端列表', async () => {
      const mockInputs = [
        { id: '1', name: 'MP4输入', type: 'file' },
        { id: '2', name: 'RTSP输入', type: 'rtsp' },
      ]
      
      vi.mocked(inputApi.getInputs).mockResolvedValue(mockInputs)
      
      await store.fetchInputs()
      
      expect(store.inputs).toEqual(mockInputs)
      expect(store.loading).toBe(false)
    })

    it('处理获取失败', async () => {
      vi.mocked(inputApi.getInputs).mockRejectedValue(new Error('网络错误'))
      
      await store.fetchInputs()
      
      expect(store.inputs).toEqual([])
      expect(store.error).toBeTruthy()
    })
  })

  describe('createInput', () => {
    it('成功创建输入端', async () => {
      const newInput = { name: '新输入', type: 'file', config: '{}' }
      const createdInput = { id: '3', ...newInput, status: 'stopped' }
      
      vi.mocked(inputApi.createInput).mockResolvedValue(createdInput)
      
      await store.createInput(newInput)
      
      expect(store.inputs).toContainEqual(createdInput)
    })
  })

  describe('deleteInput', () => {
    it('成功删除输入端', async () => {
      store.inputs = [
        { id: '1', name: '输入1' },
        { id: '2', name: '输入2' },
      ]
      
      vi.mocked(inputApi.deleteInput).mockResolvedValue()
      
      await store.deleteInput('1')
      
      expect(store.inputs).toHaveLength(1)
      expect(store.inputs[0].id).toBe('2')
    })
  })
})
```

#### 11.3.5 E2E 测试示例

```typescript
// web/tests/e2e/input.cy.ts
describe('输入端管理', () => {
  beforeEach(() => {
    cy.visit('/inputs')
  })

  it('显示输入端列表', () => {
    cy.get('.input-list').should('exist')
    cy.get('.input-item').should('have.length.greaterThan', 0)
  })

  it('创建新的MP4输入端', () => {
    cy.get('.create-btn').click()
    
    cy.get('input[name="name"]').type('测试MP4')
    cy.get('select[name="type"]').select('file')
    cy.get('input[name="path"]').type('/data/test.mp4')
    cy.get('input[name="loop"]').check()
    
    cy.get('.submit-btn').click()
    
    cy.get('.el-message--success').should('be.visible')
    cy.get('.input-list').should('contain', '测试MP4')
  })

  it('启动输入端', () => {
    cy.get('.input-item')
      .contains('测试MP4')
      .parent()
      .find('.start-btn')
      .click()
    
    cy.get('.status-tag').should('contain', '运行中')
  })

  it('停止输入端', () => {
    cy.get('.input-item')
      .contains('测试MP4')
      .parent()
      .find('.stop-btn')
      .click()
    
    cy.get('.status-tag').should('contain', '已停止')
  })

  it('删除输入端', () => {
    cy.get('.input-item')
      .contains('测试MP4')
      .parent()
      .find('.delete-btn')
      .click()
    
    cy.get('.el-message-box__btns .el-button--primary').click()
    
    cy.get('.input-list').should('not.contain', '测试MP4')
  })
})
```

### 11.4 协议功能测试

#### 11.4.1 测试环境

```yaml
# test/docker-compose.test.yml
version: "3.8"
services:
  # 被测服务
  x-media:
    build: .
    ports:
      - "8080:8080"
      - "1935:1935"
      - "5544:5544"
    
  # 测试用 RTSP 服务器
  rtsp-server:
    image: aler9/rtsp-simple-server
    ports:
      - "8554:8554"
    environment:
      - RTSP_PROTOCOLS=tcp
    
  # 测试用 FFmpeg
  ffmpeg:
    image: jrottenberg/ffmpeg
    volumes:
      - ./fixtures:/fixtures
```

#### 11.4.2 MP4 文件输入测试

```bash
#!/bin/bash
# test/protocol/test_mp4_input.sh

set -e

API_URL="http://localhost:8080/api/v1"
TEST_MP4="/fixtures/test.mp4"

echo "=== MP4 文件输入测试 ==="

# 1. 创建 MP4 输入端
echo "1. 创建 MP4 输入端..."
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试MP4",
    "type": "file",
    "config": {
      "path": "'$TEST_MP4'",
      "loop": true,
      "speed": 1.0
    }
  }' | jq -r '.id')

echo "   输入端ID: $INPUT_ID"

# 2. 启动输入端
echo "2. 启动输入端..."
curl -s -X POST "$API_URL/inputs/$INPUT_ID/start"
sleep 2

# 3. 检查状态
echo "3. 检查状态..."
STATUS=$(curl -s "$API_URL/inputs/$INPUT_ID" | jq -r '.status')
if [ "$STATUS" = "running" ]; then
    echo "   ✓ 状态正常: running"
else
    echo "   ✗ 状态异常: $STATUS"
    exit 1
fi

# 4. 检查统计信息
echo "4. 检查统计信息..."
STATS=$(curl -s "$API_URL/stats/inputs/$INPUT_ID")
echo "   统计数据: $STATS"

# 5. 停止输入端
echo "5. 停止输入端..."
curl -s -X POST "$API_URL/inputs/$INPUT_ID/stop"
sleep 1

# 6. 删除输入端
echo "6. 删除输入端..."
curl -s -X DELETE "$API_URL/inputs/$INPUT_ID"

echo "=== MP4 文件输入测试完成 ==="
```

#### 11.4.3 RTSP 输入测试

```bash
#!/bin/bash
# test/protocol/test_rtsp_input.sh

set -e

API_URL="http://localhost:8080/api/v1"
RTSP_URL="rtsp://rtsp-server:8554/test"

echo "=== RTSP 输入测试 ==="

# 1. 启动测试流
echo "1. 启动测试 RTSP 流..."
docker-compose exec ffmpeg \
  -re -i /fixtures/test.mp4 \
  -c copy -f rtsp "$RTSP_URL" &
FFMPEG_PID=$!
sleep 3

# 2. 创建 RTSP 输入端
echo "2. 创建 RTSP 输入端..."
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试RTSP",
    "type": "rtsp",
    "config": {
      "url": "'$RTSP_URL'",
      "transport": "tcp",
      "timeout_ms": 10000,
      "reconnect": true
    }
  }' | jq -r '.id')

echo "   输入端ID: $INPUT_ID"

# 3. 启动输入端
echo "3. 启动输入端..."
curl -s -X POST "$API_URL/inputs/$INPUT_ID/start"
sleep 5

# 4. 检查状态
echo "4. 检查状态..."
STATUS=$(curl -s "$API_URL/inputs/$INPUT_ID" | jq -r '.status')
if [ "$STATUS" = "running" ]; then
    echo "   ✓ 状态正常: running"
else
    echo "   ✗ 状态异常: $STATUS"
    exit 1
fi

# 5. 测试重连（模拟断流）
echo "5. 测试重连..."
kill $FFMPEG_PID 2>/dev/null || true
sleep 2
docker-compose exec ffmpeg \
  -re -i /fixtures/test.mp4 \
  -c copy -f rtsp "$RTSP_URL" &
sleep 3

STATUS=$(curl -s "$API_URL/inputs/$INPUT_ID" | jq -r '.status')
if [ "$STATUS" = "running" ]; then
    echo "   ✓ 重连成功"
else
    echo "   ✗ 重连失败"
fi

# 6. 清理
echo "6. 清理..."
curl -s -X POST "$API_URL/inputs/$INPUT_ID/stop"
curl -s -X DELETE "$API_URL/inputs/$INPUT_ID"
kill $FFMPEG_PID 2>/dev/null || true

echo "=== RTSP 输入测试完成 ==="
```

#### 11.4.4 RTMP 输出测试

```bash
#!/bin/bash
# test/protocol/test_rtmp_output.sh

set -e

API_URL="http://localhost:8080/api/v1"
RTMP_URL="rtmp://localhost:1935/live/test_output"

echo "=== RTMP 输出测试 ==="

# 1. 创建 MP4 输入端
echo "1. 创建输入端..."
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试输入",
    "type": "file",
    "config": {"path": "/fixtures/test.mp4", "loop": true}
  }' | jq -r '.id')

# 2. 创建 RTMP 输出端
echo "2. 创建 RTMP 输出端..."
OUTPUT_ID=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试RTMP输出",
    "type": "rtmp",
    "config": {
      "url": "'$RTMP_URL'",
      "reconnect": true,
      "reconnect_interval_ms": 5000
    }
  }' | jq -r '.id')

# 3. 创建管道
echo "3. 创建管道..."
PIPE_ID=$(curl -s -X POST "$API_URL/pipes" \
  -H "Content-Type: application/json" \
  -d '{
    "input_id": "'$INPUT_ID'",
    "output_id": "'$OUTPUT_ID'",
    "auto_start": true
  }' | jq -r '.id')

sleep 5

# 4. 验证 RTMP 流
echo "4. 验证 RTMP 流..."
ffprobe -v quiet -print_format json -show_streams "$RTMP_URL" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "   ✓ RTMP 流正常"
else
    echo "   ✗ RTMP 流异常"
    exit 1
fi

# 5. 检查统计
echo "5. 检查统计信息..."
STATS=$(curl -s "$API_URL/stats/pipes/$PIPE_ID")
echo "   统计数据: $STATS"

# 6. 清理
echo "6. 清理..."
curl -s -X DELETE "$API_URL/pipes/$PIPE_ID"
curl -s -X DELETE "$API_URL/outputs/$OUTPUT_ID"
curl -s -X DELETE "$API_URL/inputs/$INPUT_ID"

echo "=== RTMP 输出测试完成 ==="
```

#### 11.4.5 RTSP 输出测试

```bash
#!/bin/bash
# test/protocol/test_rtsp_output.sh

set -e

API_URL="http://localhost:8080/api/v1"
RTSP_OUTPUT_URL="rtsp://localhost:5544/live/test_output"

echo "=== RTSP 输出测试 ==="

# 1. 创建输入端
echo "1. 创建输入端..."
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试输入",
    "type": "file",
    "config": {"path": "/fixtures/test.mp4", "loop": true}
  }' | jq -r '.id')

# 2. 创建 RTSP 输出端（server 模式）
echo "2. 创建 RTSP 输出端..."
OUTPUT_ID=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试RTSP输出",
    "type": "rtsp",
    "config": {
      "mode": "server",
      "addr": ":5544",
      "transport": "tcp"
    }
  }' | jq -r '.id')

# 3. 创建管道
echo "3. 创建管道..."
PIPE_ID=$(curl -s -X POST "$API_URL/pipes" \
  -H "Content-Type: application/json" \
  -d '{
    "input_id": "'$INPUT_ID'",
    "output_id": "'$OUTPUT_ID'",
    "auto_start": true
  }' | jq -r '.id')

sleep 5

# 4. 验证 RTSP 流
echo "4. 验证 RTSP 流..."
ffprobe -v quiet -print_format json -show_streams "$RTSP_OUTPUT_URL" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "   ✓ RTSP 流正常"
else
    echo "   ✗ RTSP 流异常"
    exit 1
fi

# 5. 清理
echo "5. 清理..."
curl -s -X DELETE "$API_URL/pipes/$PIPE_ID"
curl -s -X DELETE "$API_URL/outputs/$OUTPUT_ID"
curl -s -X DELETE "$API_URL/inputs/$INPUT_ID"

echo "=== RTSP 输出测试完成 ==="
```

#### 11.4.6 HTTP-FLV 输出测试

```bash
#!/bin/bash
# test/protocol/test_httpflv_output.sh

set -e

API_URL="http://localhost:8080/api/v1"
HTTPFLV_URL="http://localhost:8080/live/test_output.flv"

echo "=== HTTP-FLV 输出测试 ==="

# 1. 创建输入端
echo "1. 创建输入端..."
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试输入",
    "type": "file",
    "config": {"path": "/fixtures/test.mp4", "loop": true}
  }' | jq -r '.id')

# 2. 创建 HTTP-FLV 输出端
echo "2. 创建 HTTP-FLV 输出端..."
OUTPUT_ID=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试HTTP-FLV输出",
    "type": "http-flv",
    "config": {
      "addr": ":8080",
      "url_pattern": "/live/",
      "gop_num": 2
    }
  }' | jq -r '.id')

# 3. 创建管道
echo "3. 创建管道..."
PIPE_ID=$(curl -s -X POST "$API_URL/pipes" \
  -H "Content-Type: application/json" \
  -d '{
    "input_id": "'$INPUT_ID'",
    "output_id": "'$OUTPUT_ID'",
    "auto_start": true
  }' | jq -r '.id')

sleep 5

# 4. 验证 HTTP-FLV 流
echo "4. 验证 HTTP-FLV 流..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$HTTPFLV_URL")
if [ "$HTTP_CODE" = "200" ]; then
    echo "   ✓ HTTP-FLV 流正常 (HTTP $HTTP_CODE)"
else
    echo "   ✗ HTTP-FLV 流异常 (HTTP $HTTP_CODE)"
    exit 1
fi

# 5. 验证 FLV 格式
echo "5. 验证 FLV 格式..."
ffprobe -v quiet -print_format json -show_streams "$HTTPFLV_URL" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "   ✓ FLV 格式正确"
else
    echo "   ✗ FLV 格式错误"
fi

# 6. 清理
echo "6. 清理..."
curl -s -X DELETE "$API_URL/pipes/$PIPE_ID"
curl -s -X DELETE "$API_URL/outputs/$OUTPUT_ID"
curl -s -X DELETE "$API_URL/inputs/$INPUT_ID"

echo "=== HTTP-FLV 输出测试完成 ==="
```

### 11.5 集成测试

#### 11.5.1 管道转发测试

```bash
#!/bin/bash
# test/integration/test_pipe_flow.sh

set -e

API_URL="http://localhost:8080/api/v1"

echo "=== 管道转发集成测试 ==="

# 测试场景：MP4 -> RTMP + RTSP + HTTP-FLV 多路输出

# 1. 创建输入端
INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "集成测试输入",
    "type": "file",
    "config": {"path": "/fixtures/test.mp4", "loop": true}
  }' | jq -r '.id')

# 2. 创建多个输出端
RTMP_OUTPUT=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{"name":"RTMP","type":"rtmp","config":{"url":"rtmp://localhost:1935/live/test"}}' | jq -r '.id')

RTSP_OUTPUT=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{"name":"RTSP","type":"rtsp","config":{"mode":"server",":5544"}}' | jq -r '.id')

HTTPFLV_OUTPUT=$(curl -s -X POST "$API_URL/outputs" \
  -H "Content-Type: application/json" \
  -d '{"name":"HTTP-FLV","type":"http-flv","config":{"addr":":8080"}}' | jq -r '.id')

# 3. 创建管道
for OUTPUT_ID in $RTMP_OUTPUT $RTSP_OUTPUT $HTTPFLV_OUTPUT; do
  curl -s -X POST "$API_URL/pipes" \
    -H "Content-Type: application/json" \
    -d '{
      "input_id": "'$INPUT_ID'",
      "output_id": "'$OUTPUT_ID'",
      "auto_start": true
    }'
done

sleep 10

# 4. 验证所有输出
echo "验证 RTMP..."
curl -s -o /dev/null -w "%{http_code}" "rtmp://localhost:1935/live/test" || true

echo "验证 RTSP..."
ffprobe -v quiet rtsp://localhost:5544/live/test || true

echo "验证 HTTP-FLV..."
curl -s -o /dev/null -w "%{http_code}" "http://localhost:8080/live/test.flv" || true

# 5. 检查统计
curl -s "$API_URL/stats" | jq .

echo "=== 管道转发集成测试完成 ==="
```

### 11.6 性能测试

#### 11.6.1 并发流测试

```bash
#!/bin/bash
# test/performance/test_concurrent_streams.sh

set -e

API_URL="http://localhost:8080/api/v1"
STREAM_COUNT=${1:-10}

echo "=== 并发流性能测试 ==="
echo "测试流数量: $STREAM_COUNT"

# 创建多个输入输出管道
for i in $(seq 1 $STREAM_COUNT); do
  INPUT_ID=$(curl -s -X POST "$API_URL/inputs" \
    -H "Content-Type: application/json" \
    -d '{
      "name": "性能测试输入'$i'",
      "type": "file",
      "config": {"path": "/fixtures/test.mp4", "loop": true}
    }' | jq -r '.id')
  
  OUTPUT_ID=$(curl -s -X POST "$API_URL/outputs" \
    -H "Content-Type: application/json" \
    -d '{
      "name": "性能测试输出'$i'",
      "type": "rtmp",
      "config": {"url": "rtmp://localhost:1935/perf/test'$i'"}
    }' | jq -r '.id')
  
  curl -s -X POST "$API_URL/pipes" \
    -H "Content-Type: application/json" \
    -d '{
      "input_id": "'$INPUT_ID'",
      "output_id": "'$OUTPUT_ID'",
      "auto_start": true
    }'
done

echo "等待流稳定..."
sleep 30

# 监控资源使用
echo "=== 资源使用情况 ==="
docker stats --no-stream x-media

# 获取统计信息
echo "=== 流统计信息 ==="
curl -s "$API_URL/stats" | jq .

echo "=== 并发流性能测试完成 ==="
```

### 11.7 测试执行

#### 11.7.1 Makefile 配置

```makefile
# Makefile

.PHONY: test test-unit test-protocol test-integration test-perf test-all

# 运行所有单元测试
test-unit:
	@echo "运行后端单元测试..."
	cd server && go test -v -coverprofile=coverage.out ./...
	@echo "运行前端单元测试..."
	cd web && npm run test:unit

# 运行协议测试
test-protocol:
	@echo "运行协议测试..."
	@bash test/protocol/test_mp4_input.sh
	@bash test/protocol/test_rtsp_input.sh
	@bash test/protocol/test_rtmp_output.sh
	@bash test/protocol/test_rtsp_output.sh
	@bash test/protocol/test_httpflv_output.sh

# 运行集成测试
test-integration:
	@echo "运行集成测试..."
	@bash test/integration/test_pipe_flow.sh

# 运行性能测试
test-perf:
	@echo "运行性能测试..."
	@bash test/performance/test_concurrent_streams.sh 10

# 运行所有测试
test-all: test-unit test-protocol test-integration

# 生成测试覆盖率报告
coverage:
	cd server && go tool cover -html=coverage.out -o coverage.html
	cd web && npm run test:coverage

# 启动测试环境
test-env-up:
	docker-compose -f test/docker-compose.test.yml up -d

# 停止测试环境
test-env-down:
	docker-compose -f test/docker-compose.test.yml down
```

#### 11.7.2 CI/CD 配置

```yaml
# .github/workflows/test.yml
name: Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main]

jobs:
  unit-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Run backend tests
        run: |
          cd server
          go test -v -coverprofile=coverage.out ./...
      
      - name: Set up Node
        uses: actions/setup-node@v3
        with:
          node-version: '18'
      
      - name: Run frontend tests
        run: |
          cd web
          npm install
          npm run test:unit

  protocol-test:
    runs-on: ubuntu-latest
    needs: unit-test
    steps:
      - uses: actions/checkout@v3
      
      - name: Start test environment
        run: make test-env-up
      
      - name: Run protocol tests
        run: make test-protocol
      
      - name: Stop test environment
        if: always()
        run: make test-env-down

  integration-test:
    runs-on: ubuntu-latest
    needs: protocol-test
    steps:
      - uses: actions/checkout@v3
      
      - name: Start test environment
        run: make test-env-up
      
      - name: Run integration tests
        run: make test-integration
      
      - name: Stop test environment
        if: always()
        run: make test-env-down
```

### 11.8 测试报告

测试完成后生成以下报告：

| 报告类型 | 生成工具 | 输出位置 |
|----------|----------|----------|
| 单元测试覆盖率 | go tool cover | server/coverage.html |
| 前端测试覆盖率 | vitest | web/coverage/ |
| 协议测试日志 | bash | test/reports/protocol.log |
| 性能测试报告 | 自定义脚本 | test/reports/performance.json |
