# X-Media Server

X-Media 是一个媒体流处理平台后端服务，支持多种媒体协议的输入输出。

## 功能特性

- 支持多种输入协议：本地文件（MP4）、RTSP
- 支持多种输出协议：RTMP、RTSP、HTTP-FLV
- 管道连接：输入输出流之间的灵活连接
- RESTful API：完整的CRUD操作
- 统计信息：实时流量统计
- 日志管理：结构化日志输出

## 项目结构

```
server/
├── cmd/                    # 入口文件
│   └── main.go
├── internal/               # 内部包
│   ├── api/                # API处理器
│   ├── config/             # 配置管理
│   ├── media/              # 媒体引擎
│   ├── model/              # 数据模型
│   ├── repository/         # 数据访问层
│   └── service/            # 业务逻辑层
├── pkg/                    # 公共包
│   ├── errors/             # 错误处理
│   ├── logger/             # 日志工具
│   └── utils/              # 工具函数
├── test/                   # 测试数据
│   └── fixtures/           # 测试文件
├── config.yaml             # 配置文件
├── go.mod                  # Go模块文件
├── Makefile                # 构建脚本
└── README.md               # 项目说明
```

## 快速开始

### 前置条件

- Go 1.21+
- SQLite

### 安装依赖

```bash
make deps
```

### 构建项目

```bash
make build
```

### 运行服务

```bash
make run
```

### 运行测试

```bash
make test
```

## API 文档

### 输入端管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/inputs | 创建输入端 |
| GET | /api/v1/inputs | 获取所有输入端 |
| GET | /api/v1/inputs/:id | 获取单个输入端 |
| PUT | /api/v1/inputs/:id | 更新输入端 |
| DELETE | /api/v1/inputs/:id | 删除输入端 |
| POST | /api/v1/inputs/:id/start | 启动输入端 |
| POST | /api/v1/inputs/:id/stop | 停止输入端 |

### 输出端管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/outputs | 创建输出端 |
| GET | /api/v1/outputs | 获取所有输出端 |
| GET | /api/v1/outputs/:id | 获取单个输出端 |
| PUT | /api/v1/outputs/:id | 更新输出端 |
| DELETE | /api/v1/outputs/:id | 删除输出端 |
| POST | /api/v1/outputs/:id/start | 启动输出端 |
| POST | /api/v1/outputs/:id/stop | 停止输出端 |

### 管道管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/pipes | 创建管道 |
| GET | /api/v1/pipes | 获取所有管道 |
| GET | /api/v1/pipes/:id | 获取单个管道 |
| DELETE | /api/v1/pipes/:id | 删除管道 |
| POST | /api/v1/pipes/:id/start | 启动管道 |
| POST | /api/v1/pipes/:id/stop | 停止管道 |

### 统计信息

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/stats | 获取总体统计 |

## 配置说明

配置文件 `config.yaml` 示例：

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
  compress: true
```

## 测试覆盖率

```bash
make test-coverage
```

生成的覆盖率报告在 `coverage.html` 文件中。

## 许可证

MIT License
