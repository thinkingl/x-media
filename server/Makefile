.PHONY: build test e2e e2e-gen clean run

# 构建项目
build:
	@echo "构建项目..."
	go build -o bin/x-media-server ./cmd/main.go

# 运行测试
test:
	@echo "运行测试..."
	go test ./... -v -count=1

# 基准参照式端到端测试（需 ffmpeg，见 docs/e2e-reference-test.md）
e2e:
	@echo "运行基准参照式端到端测试..."
	go test -tags e2e ./test/e2e/... -v -count=1

# 重新生成基准参照 fixture（test/fixtures/ref_*）
e2e-gen:
	@echo "重新生成基准参照 fixture..."
	go run ./test/e2e/gen/cmd -out test/fixtures

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
	@echo "  make e2e          - 基准参照式端到端测试（需 ffmpeg）"
	@echo "  make e2e-gen      - 重新生成基准参照 fixture"
	@echo "  make test-coverage - 运行测试并生成覆盖率报告"
	@echo "  make run          - 运行服务"
	@echo "  make clean        - 清理构建产物"
	@echo "  make fmt          - 格式化代码"
	@echo "  make lint         - 代码检查"
	@echo "  make deps         - 下载依赖"
