package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/service"
)

// TestGetLogsLinesValidation 复现 lines 参数不设防：
// 修复前 lines=0/-1 返回整份日志（内存 DoS），lines=abc 静默按 100 处理。
func TestGetLogsLinesValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewLogHandler(service.NewLogService(&config.LogConfig{
		Filename: filepath.Join(t.TempDir(), "x-media.log"),
	}))
	r := gin.New()
	r.GET("/logs", h.getLogs)

	for _, q := range []string{"lines=0", "lines=-1", "lines=abc", "lines=100001"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs?"+q, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s 必须返回 400", q)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs?lines=100", nil))
	require.Equal(t, http.StatusOK, rec.Code, "合法 lines 应放行")
}
