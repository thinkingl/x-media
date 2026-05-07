package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	HTTPAddr string
}

// Server API服务器
type Server struct {
	config     *ServerConfig
	engine     *gin.Engine
	httpServer *http.Server
	inputSvc   *service.InputService
	outputSvc  *service.OutputService
	pipeSvc    *service.PipeService
}

// NewServer 创建API服务器
func NewServer(
	config *ServerConfig,
	inputSvc *service.InputService,
	outputSvc *service.OutputService,
	pipeSvc *service.PipeService,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	// 添加中间件
	engine.Use(gin.Logger())
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())

	s := &Server{
		config:    config,
		engine:    engine,
		inputSvc:  inputSvc,
		outputSvc: outputSvc,
		pipeSvc:   pipeSvc,
	}

	// 注册路由
	s.registerRoutes()

	return s
}

// Start 启动服务器
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr,
		Handler: s.engine,
	}

	return s.httpServer.ListenAndServe()
}

// Stop 停止服务器
func (s *Server) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

// registerRoutes 注册路由
func (s *Server) registerRoutes() {
	api := s.engine.Group("/api/v1")
	{
		// 输入端
		inputs := api.Group("/inputs")
		{
			inputs.POST("", s.createInput)
			inputs.GET("", s.getInputs)
			inputs.GET("/:id", s.getInput)
			inputs.PUT("/:id", s.updateInput)
			inputs.DELETE("/:id", s.deleteInput)
			inputs.POST("/:id/start", s.startInput)
			inputs.POST("/:id/stop", s.stopInput)
		}

		// 输出端
		outputs := api.Group("/outputs")
		{
			outputs.POST("", s.createOutput)
			outputs.GET("", s.getOutputs)
			outputs.GET("/:id", s.getOutput)
			outputs.PUT("/:id", s.updateOutput)
			outputs.DELETE("/:id", s.deleteOutput)
			outputs.POST("/:id/start", s.startOutput)
			outputs.POST("/:id/stop", s.stopOutput)
		}

		// 管道
		pipes := api.Group("/pipes")
		{
			pipes.POST("", s.createPipe)
			pipes.GET("", s.getPipes)
			pipes.GET("/:id", s.getPipe)
			pipes.DELETE("/:id", s.deletePipe)
			pipes.POST("/:id/start", s.startPipe)
			pipes.POST("/:id/stop", s.stopPipe)
		}

		// 统计
		api.GET("/stats", s.getStats)
	}
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// response 统一响应
func response(c *gin.Context, code int, data interface{}) {
	c.JSON(code, gin.H{
		"code":    code,
		"message": "success",
		"data":    data,
	})
}

// errorResponse 错误响应
func errorResponse(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"code":    code,
		"message": message,
		"data":    nil,
	})
}
