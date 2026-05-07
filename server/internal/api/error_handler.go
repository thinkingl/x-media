package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/x-media/x-media-server/pkg/errors"
)

// handleError 处理错误
func handleError(c *gin.Context, err error) {
	switch e := err.(type) {
	case *errors.AppError:
		errorResponse(c, e.Code, e.Message)
	default:
		errorResponse(c, http.StatusInternalServerError, "内部服务器错误")
	}
}
