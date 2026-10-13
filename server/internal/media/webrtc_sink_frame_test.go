package media

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebRTCSinkDoesNotMutateSharedFrame 复现共享 Frame 被原地改写：
// 关键帧路径此前执行 f.Payload = s.lastKeyframe —— 同一 *Frame 由 source
// fan-out 顺序投递给多个 pipe/sink，改写入参会污染其它并发消费者的数据面。
func TestWebRTCSinkDoesNotMutateSharedFrame(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_frame_share", Type: "webrtc"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(t.Context()))
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04,
	}
	f := testFrame(0, FrameTypeVideo, CodecH264, 3000, frameData)
	f.Header.Flags = FlagKeyframe

	payloadBefore := append([]byte{}, f.Payload...)
	require.NoError(t, sink.WriteFrame(f))
	require.Equal(t, payloadBefore, f.Payload,
		"WriteFrame 不得改写共享 Frame.Payload（source fan-out 下会污染其它 sink）")

	// 非关键帧路径同样只读
	g := testFrame(0, FrameTypeVideo, CodecH264, 6000, frameData)
	payloadBefore = append([]byte{}, g.Payload...)
	require.NoError(t, sink.WriteFrame(g))
	require.Equal(t, payloadBefore, g.Payload)
}
