package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

// TestUploadHandlerBodyLimit 验证 handler 入口的 MaxBytesReader：
// 超限请求在 multipart 解析前被拒绝为 413，不进入内存解析。
func TestUploadHandlerBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.NewFileService(filepath.Join(t.TempDir(), "uploads"), nil)
	svc.MaxUploadBytes = 1024
	h := NewFileHandler(svc)
	r := gin.New()
	r.POST("/upload", h.upload)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "big.mp4")
	require.NoError(t, err)
	_, err = fw.Write(make([]byte, 4096))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
		"超出 MaxUploadBytes 的上传必须返回 413")
}
