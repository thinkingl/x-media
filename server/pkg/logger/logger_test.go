package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
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
