package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

type LogHandler struct {
	logSvc *service.LogService
}

func NewLogHandler(logSvc *service.LogService) *LogHandler {
	return &LogHandler{logSvc: logSvc}
}

func (h *LogHandler) getLogs(c *gin.Context) {
	linesStr := c.DefaultQuery("lines", "100")
	lines, err := strconv.Atoi(linesStr)
	if err != nil {
		lines = 100
	}

	logs, err := h.logSvc.GetLogsJSON(lines)
	if err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, logs)
}

func (h *LogHandler) getLogConfig(c *gin.Context) {
	cfg := h.logSvc.GetConfig()
	response(c, http.StatusOK, cfg)
}

func (h *LogHandler) updateLogConfig(c *gin.Context) {
	var req service.LogConfigResponse
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, http.StatusBadRequest, "请求参数错误: "+err.Error())
		return
	}

	if err := h.logSvc.UpdateConfig(&req); err != nil {
		handleError(c, err)
		return
	}

	response(c, http.StatusOK, h.logSvc.GetConfig())
}
