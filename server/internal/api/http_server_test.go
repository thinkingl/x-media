package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/service"
)

// TestAPIBodyLimit 复现 API JSON body 无大小限制：
// 修复前超大 body 直接进入 JSON 解析（内存耗尽），且能成功修改配置。
func TestAPIBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logSvc := service.NewLogService(&config.LogConfig{Level: "info"})
	s := NewServer(&ServerConfig{APIKey: "test-key"}, nil, nil, nil, nil, nil,
		NewLogHandler(logSvc), nil)

	// 2MB 前置空白 + 试图修改配置的合法 JSON
	body := strings.Repeat(" ", 2*1024*1024) + `{"level":"debug"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/logs/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)

	require.NotEqual(t, http.StatusOK, rec.Code,
		"超过 body 上限的 API 请求必须被拒绝")

	// 配置不应被超限请求修改
	rec = doGet(t, s, "/api/v1/logs/config", "test-key")
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Level string `json:"level"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "info", resp.Data.Level, "超限请求不得修改日志配置")
}

// TestHTTPServerTimeoutConfig 防回归：header 阶段必须有界，且
// Read/WriteTimeout 必须保持无限（否则会掐断 /live/*、/uploads 长响应）。
func TestHTTPServerTimeoutConfig(t *testing.T) {
	srv := newHTTPServer(":0", nil)
	assert.Greater(t, srv.ReadHeaderTimeout, time.Duration(0), "ReadHeaderTimeout 必须设置（slowloris 防护）")
	assert.Greater(t, srv.IdleTimeout, time.Duration(0), "IdleTimeout 必须设置")
	assert.Greater(t, srv.MaxHeaderBytes, 0, "MaxHeaderBytes 必须有界")
	assert.Zero(t, srv.ReadTimeout, "ReadTimeout 必须为 0（流媒体长响应）")
	assert.Zero(t, srv.WriteTimeout, "WriteTimeout 必须为 0（流媒体长响应）")
}

// TestSlowlorisHeaderTimeout 行为验证：只发半行 header 的连接
// 必须在 ReadHeaderTimeout 内被服务端主动关闭。
func TestSlowlorisHeaderTimeout(t *testing.T) {
	srv := newHTTPServer(":0", http.NewServeMux())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	// 发送不完整的 header 行，然后静默
	_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x"))
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(8*time.Second)))
	buf := make([]byte, 16)
	_, err = conn.Read(buf)
	require.Error(t, err, "服务端应在 ReadHeaderTimeout(5s) 内处理不完整 header 连接")
	// 服务端主动关闭 → EOF/RST；若是客户端自己的 8s 读超时则说明服务端没关
	assert.NotContains(t, err.Error(), "i/o timeout",
		"连接应在客户端读超时前被服务端关闭（ReadHeaderTimeout=5s < 8s）: %v", err)
}
