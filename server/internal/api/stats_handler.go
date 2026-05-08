package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/internal/service"
)

type StatsHandler struct {
	statsSvc *service.StatsService
}

func NewStatsHandler(statsSvc *service.StatsService) *StatsHandler {
	return &StatsHandler{statsSvc: statsSvc}
}

func (h *StatsHandler) getStats(c *gin.Context) {
	stats, err := h.statsSvc.GetOverview()
	if err != nil {
		handleError(c, err)
		return
	}
	response(c, http.StatusOK, stats)
}
