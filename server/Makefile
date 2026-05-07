.PHONY: build test clean run

# 构建项目
build:
	@echo "构建项目..."
	go build -o bin/x-media-server ./cmd/main.go

# 运行测试
test:
	@echo "运行测试..."
	go test ./... -v -count=1

# 运行测试并生成覆盖率报告
test-coverage:
	@echo "运行测试并生成覆盖率报告..."
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# 运行服务
run: build
	@echo "启动服务..."
	./bin/x-media-server -c config.yaml

# 清理构建产物
clean:
	@echo "清理构建产物..."
	rm -rf bin/
	rm -f coverage.out coverage.html

# 格式化代码
fmt:
	@echo "格式化代码..."
	go fmt ./...

# 代码检查
lint:
	@echo "代码检查..."
	go vet ./...

# 下载依赖
deps:
	@echo "下载依赖..."
	go mod tidy

# 帮助
help:
	@echo "可用命令:"
	@echo "  make build        - 构建项目"
	@echo "  make test         - 运行测试"
	@echo "  make test-coverage - 运行测试并生成覆盖率报告"
	@echo "  make run          - 运行服务"
	@echo "  make clean        - 清理构建产物"
	@echo "  make fmt          - 格式化代码"
	@echo "  make lint         - 代码检查"
	@echo "  make deps         - 下载依赖"
