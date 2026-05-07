package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

// createPipe 创建管道
func (s *Server) createPipe(c *gin.Context) {
	var req service.CreatePipeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	pipe, err := s.pipeSvc.Create(&req)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusCreated, pipe)
}

// getPipes 获取所有管道
func (s *Server) getPipes(c *gin.Context) {
	pipes, err := s.pipeSvc.GetAll()
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, pipes)
}

// getPipe 获取单个管道
func (s *Server) getPipe(c *gin.Context) {
	id := c.Param("id")

	pipe, err := s.pipeSvc.GetByID(id)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, pipe)
}

// deletePipe 删除管道
func (s *Server) deletePipe(c *gin.Context) {
	id := c.Param("id")

	if err := s.pipeSvc.Delete(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "删除成功"})
}

// startPipe 启动管道
func (s *Server) startPipe(c *gin.Context) {
	id := c.Param("id")

	if err := s.pipeSvc.Start(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "启动成功"})
}

// stopPipe 停止管道
func (s *Server) stopPipe(c *gin.Context) {
	id := c.Param("id")

	if err := s.pipeSvc.Stop(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "停止成功"})
}

// getStats 获取统计信息
func (s *Server) getStats(c *gin.Context) {
	// TODO: 实现统计功能
	stats := gin.H{
		"total_bytes_in":  0,
		"total_bytes_out": 0,
		"active_inputs":   0,
		"active_outputs":  0,
		"active_pipes":    0,
	}

	response(c, http.StatusOK, stats)
}
