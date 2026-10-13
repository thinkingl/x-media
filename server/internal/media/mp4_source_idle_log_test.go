package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestMP4SourceIdleLogRateLimited 复现 idle 日志刷屏：
// 暂停（全部 track play=false）时 readLoop 每个节流周期打一条 Warn
// （有视频 30 条/秒、纯音频可达 200 条/秒），无限刷盘。
func TestMP4SourceIdleLogRateLimited(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { _ = logger.Init(&config.LogConfig{Level: "info", Filename: ""}) })

	src, err := NewMP4Source(&InputConfig{
		ID:   "idle_log_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	defer src.Stop()

	// 先正常产帧
	time.Sleep(300 * time.Millisecond)

	// 暂停全部 track → 进入 idle
	_, err = src.Signal(ctx, &Signal{Type: SignalPause})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)

	idleWarns := logs.FilterMessageSnippet(`MP4 source idle`).Len()
	assert.Equal(t, 1, idleWarns,
		"暂停期间 idle Warn 应只有一条状态迁移日志（旧代码每节流周期一条）")

	// 恢复播放 → 应有一条恢复日志
	_, err = src.Signal(ctx, &Signal{Type: SignalResume})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet(`MP4 source resumed`).Len() >= 1
	}, 2*time.Second, 50*time.Millisecond,
		"恢复产帧应打一条恢复日志")
}
