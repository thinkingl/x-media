package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRTSPInput_RelayFromRTSPSink 真实 RTSP 中继：RTSPSink(server) 推流，RTSPInput 拉流。
func TestRTSPInput_RelayFromRTSPSink(t *testing.T) {
	// 起一个真实 RTSP server sink
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "relay_server_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: "127.0.0.1:0",
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))

	// RTSP 输入拉流
	input, err := NewRTSPInput(&InputConfig{
		ID:   "relay_input_" + t.Name(),
		Type: "rtsp",
		URL:  "rtsp://" + sinkListenerAddr(sink) + "/live/" + sink.ID(),
	})
	require.NoError(t, err)

	// 记录输入产出的帧
	recvSink := NewMockSink("relay_recv")
	pipe := NewDefaultPipe(64)
	require.NoError(t, pipe.Bind(input, recvSink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, input.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// 向 server sink 推帧
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	for i := 0; i < 30; i++ {
		sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr))
		time.Sleep(30 * time.Millisecond)
	}

	waitCond(t, 5*time.Second, func() bool {
		return recvSink.FrameCount() > 0
	}, "RTSP input relayed frames")

	frames := recvSink.Frames()
	assert.Greater(t, len(frames), 0, "should receive relayed frames")
	for _, f := range frames {
		assert.Equal(t, FrameTypeVideo, f.Header.FrameType)
		assert.Equal(t, CodecH264, f.Header.Codec)
		assert.NotEmpty(t, f.Payload)
	}
	t.Logf("relayed %d frames", len(frames))
}

// sinkListenerAddr 获取 RTSP server sink 实际监听的地址（用于拉流）。
func sinkListenerAddr(sink *RTSPSink) string {
	sink.mu.RLock()
	server := sink.handler
	sink.mu.RUnlock()
	if server == nil || server.server == nil {
		return "127.0.0.1:0"
	}
	if l := server.server.NetListener(); l != nil {
		return l.Addr().String()
	}
	return server.server.RTSPAddress
}
