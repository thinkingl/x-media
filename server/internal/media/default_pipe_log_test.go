package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// failSink 恒定写失败的 Sink（模拟 RTMP 对端断开等持续故障）。
type failSink struct{ id string }

func (s *failSink) ID() string                    { return s.id }
func (s *failSink) Status() StreamStatus          { return StreamStatusRunning }
func (s *failSink) Start(_ context.Context) error { return nil }
func (s *failSink) Stop() error                   { return nil }
func (s *failSink) Configure(_ []StreamInfo) error {
	return nil
}
func (s *failSink) WriteFrame(_ *Frame) error { return errors.New("sink down") }
func (s *failSink) Notify(_ *Signal) error    { return nil }

// TestPipeSinkFailureLogRateLimited 复现 sink 写失败日志刷屏：
// sink 持续失败时 pipe 每帧打一条 Warn（30-60fps × N 管道 → 每秒数百条）。
func TestPipeSinkFailureLogRateLimited(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { _ = logger.Init(&config.LogConfig{Level: "info", Filename: ""}) })

	src := NewMockSource("fail_src_"+t.Name(), sampleStreams())
	p := NewDefaultPipe(256)
	require.NoError(t, p.Bind(src, &failSink{id: "fail_sink_" + t.Name()}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, p.Start(ctx))
	defer p.Stop()

	for i := 0; i < 100; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000,
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40}))
	}
	time.Sleep(200 * time.Millisecond)

	warns := logs.FilterMessageSnippet(`pipe write failed`).Len()
	assert.LessOrEqual(t, warns, 2,
		"sink 持续失败时 Warn 必须限频（旧代码每帧一条 = 100 条）")
}
