package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/service"
)

// TestListDirHandlerRejectsEscape 验证目录遍历在 HTTP 层返回 400 统一 JSON，
// 而不是 200 + 宿主机目录内容。
func TestListDirHandlerRejectsEscape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	h := NewFileHandler(service.NewFileService(filepath.Join(root, "uploads"), []string{root}))
	r := gin.New()
	r.GET("/api/v1/files/list", h.listDir)

	// 越界路径 → 400
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/files/list?path=/etc", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code, "越界路径必须返回 400")

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, http.StatusBadRequest, body.Code)
	assert.NotEmpty(t, body.Message)

	// 空路径 → 200（映射到媒体根）
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/files/list", nil))
	require.Equal(t, http.StatusOK, rec.Code, "空路径应映射到媒体根并返回 200")
}
