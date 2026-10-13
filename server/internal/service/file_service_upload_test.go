package service

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/pkg/errors"
)

func newUploadTestService(t *testing.T) *FileService {
	t.Helper()
	return NewFileService(filepath.Join(t.TempDir(), "uploads"), nil)
}

// TestUploadRejectsNonMediaExtension 复现上传无扩展名白名单：
// 修复前 evil.html 等任意文件都能落盘，并经 /uploads 静态服务以
// text/html 返回 —— 同源存储型 XSS。
func TestUploadRejectsNonMediaExtension(t *testing.T) {
	svc := newUploadTestService(t)

	for _, name := range []string{"evil.html", "evil.svg", "evil.xml", "evil.js", "noext"} {
		path, err := svc.Upload(name, bytes.NewReader([]byte("x")))
		require.Error(t, err, "文件 %q 必须被拒绝", name)
		assert.Empty(t, path)
		var appErr *errors.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, 400, appErr.Code, "非法扩展名应为 400: %q", name)
	}
}

// TestUploadAllowsMediaExtensions 保护合法用法：媒体扩展名必须可上传。
func TestUploadAllowsMediaExtensions(t *testing.T) {
	svc := newUploadTestService(t)

	for _, name := range []string{"a.mp4", "b.flv", "c.ts", "d.mkv", "e.MOV"} {
		path, err := svc.Upload(name, bytes.NewReader([]byte("x")))
		require.NoError(t, err, "媒体文件 %q 应可上传", name)
		assert.True(t, strings.HasPrefix(path, svc.uploadDir))
	}
}

// TestUploadRejectsOversize 复现上传无大小限制：修复前 io.Copy 不设上限，
// 匿名可写入任意大的文件耗尽磁盘。
func TestUploadRejectsOversize(t *testing.T) {
	svc := newUploadTestService(t)
	svc.MaxUploadBytes = 1024

	_, err := svc.Upload("big.mp4", bytes.NewReader(make([]byte, 2048)))
	require.Error(t, err)
	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, appErr.Code, "超限应为 413")

	// 半成品文件必须被清理
	entries, err := os.ReadDir(svc.uploadDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "超限上传不得残留文件")

	// 恰好等于上限应成功
	path, err := svc.Upload("ok.mp4", bytes.NewReader(make([]byte, 1024)))
	require.NoError(t, err)
	assert.NotEmpty(t, path)
}
