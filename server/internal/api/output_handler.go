package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

// createOutput 创建输出端
func (s *Server) createOutput(c *gin.Context) {
	var req service.CreateOutputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	output, err := s.outputSvc.Create(&req)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusCreated, output)
}

// getOutputs 获取所有输出端
func (s *Server) getOutputs(c *gin.Context) {
	outputs, err := s.outputSvc.GetAll()
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, outputs)
}

// getOutput 获取单个输出端
func (s *Server) getOutput(c *gin.Context) {
	id := c.Param("id")

	output, err := s.outputSvc.GetByID(id)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, output)
}

// updateOutput 更新输出端
func (s *Server) updateOutput(c *gin.Context) {
	id := c.Param("id")

	var req service.CreateOutputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	output, err := s.outputSvc.Update(id, &req)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, output)
}

// deleteOutput 删除输出端
func (s *Server) deleteOutput(c *gin.Context) {
	id := c.Param("id")

	if err := s.outputSvc.Delete(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "删除成功"})
}

// startOutput 启动输出端
func (s *Server) startOutput(c *gin.Context) {
	id := c.Param("id")

	if err := s.outputSvc.Start(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "启动成功"})
}

// stopOutput 停止输出端
func (s *Server) stopOutput(c *gin.Context) {
	id := c.Param("id")

	if err := s.outputSvc.Stop(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "停止成功"})
}
