# X-Media 媒体流处理平台

X-Media 是一个前后端分离的媒体流处理平台，支持多种媒体协议的输入输出，可以在输入输出端之间建立媒体流管道连接。

## 功能特性

- 多种输入协议：本地文件（MP4）、RTSP
- 多种输出协议：RTMP、RTSP、HTTP-FLV
- 管道连接：输入输出流之间的灵活路由
- RESTful API：完整的 CRUD 操作
- 统计信息：实时流量统计
- 日志管理：结构化日志、文件轮转、配置热更新
- Web 管理界面：Vue3 + Element Plus

## 技术栈

| 层级 | 技术 |
|------|------|
| 后端语言 | Go 1.21+ |
| Web 框架 | Gin |
| ORM | GORM |
| 数据库 | SQLite |
| 日志 | zap + lumberjack (文件轮转) |
| 前端框架 | Vue3 + TypeScript |
| UI 组件 | Element Plus |
| 构建工具 | Vite |
| 状态管理 | Pinia |

## 项目结构

```
x-media/
├── server/                     # Go 后端
│   ├── cmd/main.go             # 入口
│   ├── internal/
│   │   ├── api/                # HTTP 处理器
│   │   ├── config/             # 配置加载
│   │   ├── media/              # 媒体引擎（文件/RTSP输入，RTMP/RTSP/HTTP-FLV输出）
│   │   ├── model/              # 数据模型
│   │   ├── repository/         # 数据访问层
│   │   └── service/            # 业务逻辑层
│   ├── pkg/                    # 公共包（logger, errors, utils）
│   ├── test/fixtures/          # 测试数据
│   └── Makefile
├── web/                        # Vue3 前端
│   ├── src/
│   │   ├── api/                # API 接口
│   │   ├── components/         # 组件
│   │   ├── router/             # 路由
│   │   ├── stores/             # Pinia 状态
│   │   └── views/              # 页面（仪表盘、输入端、输出端、管道、日志）
│   └── package.json
├── docker/
│   ├── Dockerfile              # 多阶段构建（后端 + 前端）
│   └── config.yaml             # Docker 配置
├── docker-compose.yml
├── docs/design.md              # 架构设计文档
└── AGENTS.md
```

## 快速开始

### 前置条件

- Go 1.21+
- Node.js 18+
- SQLite

### 后端

```bash
cd server
make deps           # 下载依赖
make build          # 构建（需要 CGO_ENABLED=1）
make test           # 运行测试
make run            # 构建并启动服务
```

### 前端

```bash
cd web
npm install         # 安装依赖
npm run dev         # 启动开发服务器 (localhost:3000)
npm run build       # 构建生产版本
```

### Docker 部署

```bash
docker-compose up -d                # 构建并启动
docker-compose logs -f              # 查看日志
docker-compose down                 # 停止
```

访问 http://localhost:8080 进入管理界面。

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/inputs | 创建输入端 |
| GET | /api/v1/inputs | 获取所有输入端 |
| GET | /api/v1/inputs/:id | 获取单个输入端 |
| PUT | /api/v1/inputs/:id | 更新输入端 |
| DELETE | /api/v1/inputs/:id | 删除输入端 |
| POST | /api/v1/inputs/:id/start | 启动输入端 |
| POST | /api/v1/inputs/:id/stop | 停止输入端 |
| POST | /api/v1/outputs | 创建输出端 |
| GET | /api/v1/outputs | 获取所有输出端 |
| PUT | /api/v1/outputs/:id | 更新输出端 |
| DELETE | /api/v1/outputs/:id | 删除输出端 |
| POST | /api/v1/outputs/:id/start | 启动输出端 |
| POST | /api/v1/outputs/:id/stop | 停止输出端 |
| POST | /api/v1/pipes | 创建管道 |
| GET | /api/v1/pipes | 获取所有管道 |
| DELETE | /api/v1/pipes/:id | 删除管道 |
| POST | /api/v1/pipes/:id/start | 启动管道 |
| POST | /api/v1/pipes/:id/stop | 停止管道 |
| GET | /api/v1/stats | 获取统计信息 |
| GET | /api/v1/logs | 获取日志 |
| GET | /api/v1/logs/config | 获取日志配置 |
| PUT | /api/v1/logs/config | 更新日志配置 |

## 配置

配置文件 `config.yaml`：

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
  max_size: 100      # MB
  max_backups: 5
  max_age: 30         # 天
  compress: true
```

## 许可证

MIT License
