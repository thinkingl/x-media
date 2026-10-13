package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
)

// TestUpdateConfigRebuildsLogger 复现"日志配置修改不生效"：
// UpdateConfig 此前只改内存结构体，zap/lumberjack 早已构造完毕，
// UI 提示成功但级别/文件全都不变。
func TestUpdateConfigRebuildsLogger(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	initial := "initial.log"
	cfg := &config.LogConfig{Level: "info", Filename: initial}
	svc := NewLogService(cfg)
	require.NoError(t, logger.Init(cfg))
	t.Cleanup(logger.Sync)

	target := "rebuild.log"
	require.NoError(t, svc.UpdateConfig(&LogConfigResponse{Level: "debug", Filename: target}))
	logger.Debug("rebuild probe")

	data, err := os.ReadFile(target)
	require.NoError(t, err, "UpdateConfig 后日志必须写入新文件")
	assert.Contains(t, string(data), "rebuild probe",
		"UpdateConfig 的级别/文件修改必须热生效（旧代码只改内存结构体）")
}

// TestUpdateConfigRejectsUnsafeFilename 复现任意路径写日志：
// filename 未校验时，热重建后可被指定到任意可写路径。
func TestUpdateConfigRejectsUnsafeFilename(t *testing.T) {
	tmp := t.TempDir()
	initial := filepath.Join(tmp, "initial.log")
	cfg := &config.LogConfig{Level: "info", Filename: initial}
	svc := NewLogService(cfg)
	require.NoError(t, logger.Init(cfg))
	t.Cleanup(logger.Sync)

	for _, bad := range []string{"/etc/cron.d/evil", "../evil.log", "logs/../../evil.log"} {
		err := svc.UpdateConfig(&LogConfigResponse{Filename: bad})
		require.Error(t, err, "filename=%q 必须被拒绝", bad)
		var appErr *errors.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, 400, appErr.Code, "filename=%q 应为 400", bad)
		assert.Equal(t, initial, cfg.Filename, "拒绝时配置不得变化")
	}
}

// TestUpdateConfigLevelRestrictsOutput 验证降级同样生效。
func TestUpdateConfigLevelRestrictsOutput(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	cfg := &config.LogConfig{Level: "info", Filename: "initial.log"}
	svc := NewLogService(cfg)
	require.NoError(t, logger.Init(cfg))
	t.Cleanup(logger.Sync)

	target := "error.log"
	require.NoError(t, svc.UpdateConfig(&LogConfigResponse{Level: "error", Filename: target}))
	logger.Info("should not appear")
	logger.Error("error probe")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "should not appear", "降级到 error 后 Info 不应落盘")
	assert.Contains(t, string(data), "error probe")
}

// TestRebuildKeepsOldOnError 验证 Rebuild 失败时保留旧 logger。
func TestRebuildKeepsOldOnError(t *testing.T) {
	tmp := t.TempDir()
	initial := filepath.Join(tmp, "initial.log")
	require.NoError(t, logger.Init(&config.LogConfig{Level: "info", Filename: initial}))
	t.Cleanup(logger.Sync)

	old := logger.GetLogger()
	require.Error(t, logger.Rebuild(nil), "非法配置必须返回错误")
	assert.Same(t, old, logger.GetLogger(), "失败后必须保留旧 logger")

	logger.Info("still works")
	data, err := os.ReadFile(initial)
	require.NoError(t, err)
	assert.Contains(t, string(data), "still works", "旧 logger 应继续可用")
}
