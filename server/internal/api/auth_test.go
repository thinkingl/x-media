package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/service"
)

// stubEngine 仅用于让 /live/* 路由走完 GetOutput 返回 404，验证豁免路径不受鉴权拦截。
type stubEngine struct{ media.Engine }

func (stubEngine) GetOutput(string) (media.Sink, error) {
	return nil, assert.AnError
}

func newTestServer(apiKey string, corsOrigins []string) *Server {
	logHandler := NewLogHandler(service.NewLogService(&config.LogConfig{}))
	return NewServer(&ServerConfig{APIKey: apiKey, CORSOrigins: corsOrigins},
		stubEngine{}, nil, nil, nil, nil, logHandler, nil)
}

func doGet(t *testing.T, s *Server, path, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	return rec
}

// TestAPIRequiresKey 复现无鉴权问题：修复前匿名请求可直接调用管理 API。
func TestAPIRequiresKey(t *testing.T) {
	s := newTestServer("secret-key", nil)
	require.Equal(t, http.StatusUnauthorized, doGet(t, s, "/api/v1/logs/config", "").Code,
		"未携带 API Key 的管理 API 请求必须被拒绝")
	require.Equal(t, http.StatusUnauthorized, doGet(t, s, "/api/v1/logs/config", "wrong-key").Code,
		"错误的 API Key 必须被拒绝")
	require.Equal(t, http.StatusOK, doGet(t, s, "/api/v1/logs/config", "secret-key").Code,
		"携带正确 API Key 的请求必须放行")
}

// TestAPIRejectsAllWhenKeyUnconfigured 验证安全默认：未配置 api_key 时
// 任何 key 都不能通过（避免匿名暴露管理面）。
func TestAPIRejectsAllWhenKeyUnconfigured(t *testing.T) {
	s := newTestServer("", nil)
	require.Equal(t, http.StatusUnauthorized, doGet(t, s, "/api/v1/logs/config", "").Code)
	require.Equal(t, http.StatusUnauthorized, doGet(t, s, "/api/v1/logs/config", "anything").Code)
}

// TestStreamingPathsExemptFromAuth 验证拉流/静态媒体路径豁免鉴权：
// 播放器（VLC、flv.js、浏览器 video）无法携带自定义请求头。
func TestStreamingPathsExemptFromAuth(t *testing.T) {
	s := newTestServer("secret-key", nil)
	assert.Equal(t, http.StatusNotFound, doGet(t, s, "/live/nonexistent.flv", "").Code,
		"/live/* 应豁免鉴权（走到输出不存在的 404，而非 401）")
	assert.Equal(t, http.StatusNotFound, doGet(t, s, "/uploads/missing.mp4", "").Code,
		"/uploads/* 应豁免鉴权")
}

// TestCORSDisabledByDefault 验证默认不下发 CORS 头：同源部署无需 CORS，
// 不再允许任意第三方网站跨域读取本实例响应。
func TestCORSDisabledByDefault(t *testing.T) {
	s := newTestServer("secret-key", nil)
	rec := doGet(t, s, "/api/v1/logs/config", "secret-key")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"未配置 cors_origins 时不得下发 Access-Control-Allow-Origin")
}

// TestCORSWhitelist 验证 CORS 白名单：白名单内 Origin 回显，白名单外不下发。
func TestCORSWhitelist(t *testing.T) {
	s := newTestServer("secret-key", []string{"http://allowed.example"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/config", nil)
	req.Header.Set("Origin", "http://allowed.example")
	req.Header.Set("X-API-Key", "secret-key")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://allowed.example", rec.Header().Get("Access-Control-Allow-Origin"),
		"白名单内 Origin 应被回显")

	req = httptest.NewRequest(http.MethodGet, "/api/v1/logs/config", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("X-API-Key", "secret-key")
	rec = httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"白名单外 Origin 不得获得跨域头")

	// 预检请求：白名单内放行，白名单外拒绝
	req = httptest.NewRequest(http.MethodOptions, "/api/v1/logs/config", nil)
	req.Header.Set("Origin", "http://allowed.example")
	rec = httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	req = httptest.NewRequest(http.MethodOptions, "/api/v1/logs/config", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// compile-time guard: stubEngine 满足 media.Engine
var _ media.Engine = stubEngine{}
