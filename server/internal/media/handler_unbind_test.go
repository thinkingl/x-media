package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMP4SourceRemoveFrameHandler 验证帧回调解绑：
// AddFrameHandler 返回的 remove 必须使该 handler 不再收到帧（幂等），
// 否则管道反复停启时旧 sink 被闭包永久钉住（内存泄漏）。
func TestMP4SourceRemoveFrameHandler(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "rm_handler_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
	})
	require.NoError(t, err)

	var got int
	remove := src.AddFrameHandler(func(*Frame) { got++ })
	remove()
	remove() // 幂等

	frame := testFrame(0, FrameTypeVideo, CodecH264, 3000, []byte{0, 0, 0, 1, 0x65})
	src.emit(frame)
	assert.Zero(t, got, "已移除的 handler 不得再收到帧")

	// 其它 handler 不受影响（fan-out 语义）
	var other int
	_ = src.AddFrameHandler(func(*Frame) { other++ })
	src.emit(frame)
	assert.Equal(t, 1, other)
	assert.Zero(t, got)
}

// TestPipeStopUnbindsFromSource 验证管道 Stop 后解除帧订阅。
func TestPipeStopUnbindsFromSource(t *testing.T) {
	src := NewMockSource("unbind_src_"+t.Name(), sampleStreams())
	sink := NewMockSink("unbind_sink_" + t.Name())
	p := NewDefaultPipe(128)
	require.NoError(t, p.Bind(src, sink))
	require.Equal(t, 1, src.HandlerCount(), "Bind 后应注册一个帧回调")

	p.Unbind()
	p.Unbind() // 幂等
	require.Equal(t, 0, src.HandlerCount(), "Unbind 后必须解绑（旧代码恒增不减）")
}

// TestPipeStopUnbindsRegression Stop 路径也必须解绑。
func TestPipeStopUnbindsRegression(t *testing.T) {
	src := NewMockSource("unbind_stop_src_"+t.Name(), sampleStreams())
	sink := NewMockSink("unbind_stop_sink_" + t.Name())
	p := NewDefaultPipe(128)
	require.NoError(t, p.Bind(src, sink))

	require.NoError(t, src.Start(t.Context()))
	require.NoError(t, p.Start(t.Context()))
	require.NoError(t, p.Stop())
	require.Equal(t, 0, src.HandlerCount(), "Stop 后必须解绑帧回调")

	// 解绑后 Push 不再到达（不 panic、不入队）
	src.Push(testFrame(0, FrameTypeVideo, CodecH264, 3000, []byte{0, 0, 0, 1, 0x65}))
}
