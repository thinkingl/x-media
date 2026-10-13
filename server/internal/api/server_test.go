package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/logger"
)

func TestHTTPLogsUseRotatingFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "x-media.log")
	require.NoError(t, logger.Init(&config.LogConfig{Level: "info", Filename: filename}))
	t.Cleanup(logger.Sync)
	s := NewServer(&ServerConfig{}, nil, nil, nil, nil, nil, nil, nil)
	s.engine.GET("/log-test", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	s.engine.GET("/panic-test", func(c *gin.Context) { panic("test panic") })
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/log-test?token=secret-test", http.StatusNoContent},
		{"/panic-test", http.StatusInternalServerError},
	} {
		recorder := httptest.NewRecorder()
		s.engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.code, recorder.Code)
	}
	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Contains(t, string(data), "HTTP GET /log-test status=204")
	require.Contains(t, string(data), "HTTP handler panic")
	require.Contains(t, string(data), "stack")
	require.NotContains(t, string(data), "secret-test")
}
