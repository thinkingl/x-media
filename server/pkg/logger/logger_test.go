package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRotationRetention(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "x-media.log")
	cfg := &config.LogConfig{Level: "info", Filename: filename, MaxSize: 1, MaxBackups: 2, MaxAge: 1}
	stdout := os.Stdout
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = null.Close() })
	os.Stdout = null
	initErr := Init(cfg)
	os.Stdout = stdout
	require.NoError(t, initErr)
	t.Cleanup(Sync)
	payload := strings.Repeat("x", 600*1024)
	for i := 0; i < 5; i++ {
		GetLogger().Info(payload)
		time.Sleep(10 * time.Millisecond)
	}
	require.Eventually(t, func() bool {
		files, err := filepath.Glob(filepath.Join(dir, "x-media-*.log"))
		return err == nil && len(files) == 2
	}, time.Second, 10*time.Millisecond)
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	require.NoError(t, err)
	require.Len(t, files, 3)
	for _, file := range files {
		info, err := os.Stat(file)
		require.NoError(t, err)
		require.LessOrEqual(t, info.Size(), int64(1024*1024))
	}
}

func TestRotationDefaults(t *testing.T) {
	cfg := &config.LogConfig{Filename: filepath.Join(t.TempDir(), "x-media.log"), MaxSize: -1, MaxAge: -1}
	require.NoError(t, Init(cfg))
	t.Cleanup(Sync)
	require.Equal(t, 100, cfg.MaxSize)
	require.Equal(t, 5, cfg.MaxBackups)
	require.Equal(t, 30, cfg.MaxAge)
	Info("retained log")
	data, err := os.ReadFile(cfg.Filename)
	require.NoError(t, err)
	require.Contains(t, string(data), "retained log")
}

func TestSetLoggerHook(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	SetLogger(zap.New(core))
	t.Cleanup(func() { _ = Init(&config.LogConfig{Level: "info", Filename: ""}) })

	Info("hook marker")
	require.Equal(t, 1, logs.FilterMessage("hook marker").Len(),
		"SetLogger 注入的 observer 应能观测到日志（测试钩子防回归）")
}

// TestCallerPointsToCallSite 复现 caller 定位失效：
// sugar 包装层少一层 CallerSkip，所有日志 caller 恒为 pkg/logger/logger.go。
func TestCallerPointsToCallSite(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	SetLogger(zap.New(core, zap.AddCaller()))
	t.Cleanup(func() { _ = Init(&config.LogConfig{Level: "info", Filename: ""}) })

	Infof("caller probe %d", 42)
	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	require.NotNil(t, entry.Caller, "日志应带 caller")
	assert.True(t, strings.HasSuffix(entry.Caller.File, "logger_test.go"),
		"sugar 包装的 caller 应指向调用点（logger_test.go），实际: %s", entry.Caller.File)

	// With() 返回的裸 logger 不经过包装函数，caller 必须仍是使用处（防过度补偿）
	With().Info("via with")
	require.Equal(t, 2, logs.Len())
	entry2 := logs.All()[1]
	require.NotNil(t, entry2.Caller)
	assert.True(t, strings.HasSuffix(entry2.Caller.File, "logger_test.go"),
		"With() 的 caller 应指向使用处，实际: %s", entry2.Caller.File)
}
