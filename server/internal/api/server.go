package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/service"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	HTTPAddr   string
	StaticPath string
}

type httpHandler interface {
	GetRoutePath() string
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

type Server struct {
	config       *ServerConfig
	engine       *gin.Engine
	httpServer   *http.Server
	mediaEngine  media.MediaEngine
	inputSvc     *service.InputService
	outputSvc    *service.OutputService
	pipeSvc      *service.PipeService
	statsHandler *StatsHandler
	logHandler   *LogHandler
	fileHandler  *FileHandler
	flvOutputs   map[string]httpHandler
}

// NewServer 创建API服务器
func NewServer(
	config *ServerConfig,
	mediaEngine media.MediaEngine,
	inputSvc *service.InputService,
	outputSvc *service.OutputService,
	pipeSvc *service.PipeService,
	statsHandler *StatsHandler,
	logHandler *LogHandler,
	fileHandler *FileHandler,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	// 添加中间件
	engine.Use(gin.Logger())
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())

	s := &Server{
		config:       config,
		engine:       engine,
		mediaEngine:  mediaEngine,
		inputSvc:     inputSvc,
		outputSvc:    outputSvc,
		pipeSvc:      pipeSvc,
		statsHandler: statsHandler,
		logHandler:   logHandler,
		fileHandler:  fileHandler,
		flvOutputs:   make(map[string]httpHandler),
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
			inputs.POST("/:id/probe", s.probeInput)
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
			pipes.PUT("/:id", s.updatePipe)
			pipes.DELETE("/:id", s.deletePipe)
			pipes.POST("/:id/start", s.startPipe)
			pipes.POST("/:id/stop", s.stopPipe)
		}

		// 统计
		api.GET("/stats", s.statsHandler.getStats)

		// 日志
		logs := api.Group("/logs")
		{
			logs.GET("", s.logHandler.getLogs)
			logs.GET("/config", s.logHandler.getLogConfig)
			logs.PUT("/config", s.logHandler.updateLogConfig)
		}

		files := api.Group("/files")
		{
			files.GET("/list", s.fileHandler.listDir)
			files.POST("/upload", s.fileHandler.upload)
		}
	}

	// 前端静态文件
	if s.config.StaticPath != "" {
		s.engine.Static("/assets", s.config.StaticPath+"/assets")
		s.engine.StaticFile("/vite.svg", s.config.StaticPath+"/vite.svg")
		s.engine.NoRoute(func(c *gin.Context) {
			c.File(s.config.StaticPath + "/index.html")
		})
	}

	s.engine.Static("/uploads", "./uploads")

	s.engine.GET("/live/:filename", func(c *gin.Context) {
		filename := c.Param("filename")
		outputID := strings.TrimSuffix(filename, ".flv")
		out, err := s.mediaEngine.GetOutput(outputID)
		if err != nil {
			c.JSON(404, gin.H{"code": 404, "message": "output not found"})
			return
		}
		if h, ok := out.(httpHandler); ok {
			h.ServeHTTP(c.Writer, c.Request)
		} else {
			c.JSON(400, gin.H{"code": 400, "message": "output is not HTTP-FLV"})
		}
	})
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
