package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/x-media/x-media-server/internal/api"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/internal/service"
	"github.com/x-media/x-media-server/pkg/logger"
)

func main() {
	// 解析命令行参数
	configFile := flag.String("c", "config.yaml", "配置文件路径")
	staticPath := flag.String("s", "", "前端静态文件路径")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	if err := logger.Init(&cfg.Log); err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}
	defer logger.Sync()

	logger.Info("启动 X-Media Server...")

	// 初始化数据库
	db, err := repository.InitDB(&cfg.Database)
	if err != nil {
		logger.Fatalf("初始化数据库失败: %v", err)
	}

	// 初始化媒体引擎
	engine := media.NewMediaEngine()
	if err := engine.Start(context.Background()); err != nil {
		logger.Fatalf("启动媒体引擎失败: %v", err)
	}

	// 初始化仓储层
	inputRepo := repository.NewInputRepo(db)
	outputRepo := repository.NewOutputRepo(db)
	pipeRepo := repository.NewPipeRepo(db)

	// 初始化服务层
	inputSvc := service.NewInputService(inputRepo, engine)
	outputSvc := service.NewOutputService(outputRepo, engine)
	pipeSvc := service.NewPipeService(pipeRepo, inputRepo, outputRepo, engine)
	statsSvc := service.NewStatsService(inputRepo, outputRepo, pipeRepo)
	logSvc := service.NewLogService(&cfg.Log)
	fileSvc := service.NewFileService("./uploads")

	statsHandler := api.NewStatsHandler(statsSvc)
	logHandler := api.NewLogHandler(logSvc)
	fileHandler := api.NewFileHandler(fileSvc)
	server := api.NewServer(&api.ServerConfig{
		HTTPAddr:   cfg.Server.HTTPAddr,
		StaticPath: *staticPath,
	}, inputSvc, outputSvc, pipeSvc, statsHandler, logHandler, fileHandler)

	// 启动服务器
	go func() {
		if err := server.Start(); err != nil {
			logger.Fatalf("启动服务器失败: %v", err)
		}
	}()

	logger.Info(fmt.Sprintf("HTTP 服务已启动: %s", cfg.Server.HTTPAddr))

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭服务器...")

	// 停止媒体引擎
	if err := engine.Stop(); err != nil {
		logger.Errorf("停止媒体引擎失败: %v", err)
	}

	// 优雅关闭
	if err := server.Stop(); err != nil {
		logger.Errorf("关闭服务器失败: %v", err)
	}

	logger.Info("服务器已关闭")
}
