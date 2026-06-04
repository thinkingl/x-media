package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

// createInput 创建输入端
func (s *Server) createInput(c *gin.Context) {
	var req service.CreateInputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	input, err := s.inputSvc.Create(&req)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusCreated, input)
}

// getInputs 获取所有输入端
func (s *Server) getInputs(c *gin.Context) {
	inputs, err := s.inputSvc.GetAll()
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, inputs)
}

// getInput 获取单个输入端
func (s *Server) getInput(c *gin.Context) {
	id := c.Param("id")

	input, err := s.inputSvc.GetByID(id)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, input)
}

// updateInput 更新输入端
func (s *Server) updateInput(c *gin.Context) {
	id := c.Param("id")

	var req service.CreateInputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	input, err := s.inputSvc.Update(id, &req)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, input)
}

// deleteInput 删除输入端
func (s *Server) deleteInput(c *gin.Context) {
	id := c.Param("id")

	if err := s.inputSvc.Delete(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "删除成功"})
}

// startInput 启动输入端
func (s *Server) startInput(c *gin.Context) {
	id := c.Param("id")

	if err := s.inputSvc.Start(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "启动成功"})
}

// stopInput 停止输入端
func (s *Server) stopInput(c *gin.Context) {
	id := c.Param("id")

	if err := s.inputSvc.Stop(id); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, gin.H{"message": "停止成功"})
}

func (s *Server) probeInput(c *gin.Context) {
	id := c.Param("id")

	info, err := s.inputSvc.ProbeInput(id)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, info)
}
