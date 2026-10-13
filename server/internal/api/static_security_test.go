package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUploadsServeNoSniff 复现 /uploads 缺少 nosniff 头：
// 配合上传扩展名白名单，阻止浏览器对媒体文件做 MIME 嗅探执行。
func TestUploadsServeNoSniff(t *testing.T) {
	s := newTestServer("test-key", nil)
	rec := doGet(t, s, "/uploads/missing.mp4", "")
	require.Equal(t, http.StatusNotFound, rec.Code, "缺失文件应为 404（测试只验证响应头）")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"),
		"/uploads 响应必须带 X-Content-Type-Options: nosniff")
}
