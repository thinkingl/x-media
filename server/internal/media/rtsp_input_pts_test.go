package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRTSPInput_PTSForFragmentedIDR 验证大关键帧（RTP FU-A 分片）也能得到非零 PTS。
//
// 复现场景：SPS/PPS 只通过 SDP 下发，IDR 帧远大于 MTU，被拆成多个 FU-A 分片。
// 旧实现只在“组装完一个 AU 的最后一个 RTP 包”上调用 PacketPTS；该包是 FU-A
// 结束分片，PTSEqualsDTS=false，导致 timeDecoder 永不初始化，所有帧 PTS=0。
func TestRTSPInput_PTSForFragmentedIDR(t *testing.T) {
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "frag_server_" + t.Name(),
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

	input, err := NewRTSPInput(&InputConfig{
		ID:   "frag_input_" + t.Name(),
		Type: "rtsp",
		URL:  "rtsp://" + sinkListenerAddr(sink) + "/live/" + sink.ID(),
	})
	require.NoError(t, err)

	recvSink := NewMockSink("frag_recv")
	pipe := NewDefaultPipe(256)
	require.NoError(t, pipe.Bind(input, recvSink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, input.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// 100 KB 的单个 IDR NAL：必然被 rtp 层拆成 FU-A 分片。
	idr := make([]byte, 100_000)
	copy(idr, []byte{0x00, 0x00, 0x00, 0x01, 0x65})
	for i := 0; i < 5; i++ {
		require.NoError(t, sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr)))
		time.Sleep(50 * time.Millisecond)
	}

	waitCond(t, 5*time.Second, func() bool {
		return recvSink.FrameCount() > 0
	}, "RTSP input receives fragmented IDR frames")

	frames := recvSink.Frames()
	require.NotEmpty(t, frames)
	// timeDecoder 从 0 起算，首帧 PTS=0 是正常的；关键是后续帧不能全为 0。
	require.Greater(t, frames[len(frames)-1].Header.PTS, int64(0),
		"fragmented-IDR PTS must be initialized from RTP timestamp")
	for i := 1; i < len(frames); i++ {
		assert.GreaterOrEqual(t, frames[i].Header.PTS, frames[i-1].Header.PTS,
			"PTS must not move backwards across fragmented-IDR frames")
	}
	t.Logf("received %d frames, first PTS=%d last PTS=%d", len(frames), frames[0].Header.PTS, frames[len(frames)-1].Header.PTS)
}
