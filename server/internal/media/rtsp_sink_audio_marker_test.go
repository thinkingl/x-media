package media

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRTSPSink_AudioMarkerBit 防回归：音频 RTP 包必须设置 Marker 位。
//
// live555（VLC 的 RTSP 实现）以 Marker 位判定“本包是否构成完整帧”：
// MultiFramedRTPSource::doGetNextFrame1() 仅当 fCurrentPacketCompletesFrame
// 为真才向上层交付帧，而 MPEG4GenericRTPSource::processSpecialHeader() 中
// 该字段直接取自 packet->rtpMarkerBit()。此前 writeAudio 未设置 Marker，
// live555 视每包为“未完成帧的一部分”，永远不交付任何音频帧——表现为
// VLC 统计只有视频、无音频（ffmpeg 的 rtpdec_mpeg4.c 不依赖 Marker，
// 因此测试全绿也无法暴露该问题）。
func TestRTSPSink_AudioMarkerBit(t *testing.T) {
	aacConfig := []byte{0x11, 0x90}
	sink := newRTSPTestSink(t)
	defer sink.Stop()

	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000,
			CodecConfig: []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
				0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 48000, CodecConfig: aacConfig},
	}))
	require.True(t, sink.ready)

	// 真实 RTSP 客户端接收，逐包检查 RTP Header（与 live555 的检查点一致）。
	// 注意：OnPacketRTPAny 只对已 SETUP 的媒体注册回调，必须在 SetupAll 之后调用。
	audioMarkers := make(chan bool, 64)
	client := &gortsplib.Client{Scheme: "rtsp", Host: sink.Addr()}
	require.NoError(t, client.Start())
	defer client.Close()

	u, err := base.ParseURL("rtsp://" + sink.Addr() + "/live/" + sink.ID())
	require.NoError(t, err)
	desc, _, err := client.Describe(u)
	require.NoError(t, err)
	require.NoError(t, client.SetupAll(desc.BaseURL, desc.Medias))
	client.OnPacketRTPAny(func(media *description.Media, _ format.Format, pkt *rtp.Packet) {
		if media.Type == description.MediaTypeAudio {
			select {
			case audioMarkers <- pkt.Marker:
			default:
			}
		}
	})
	_, err = client.Play(nil)
	require.NoError(t, err)

	// 持续写音频帧，等待客户端收到第一个音频 RTP 包
	var got *bool
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; time.Now().Before(deadline) && got == nil; i++ {
		require.NoError(t, sink.WriteFrame(
			testFrame(1, FrameTypeAudio, CodecAAC, int64(i)*1024, []byte{0xDE, 0xAD, 0xBE, 0xEF})))
		select {
		case m := <-audioMarkers:
			got = &m
		case <-time.After(20 * time.Millisecond):
		}
	}

	require.NotNil(t, got, "客户端应至少收到一个音频 RTP 包")
	assert.True(t, *got, "音频 RTP 包必须设置 Marker 位（live555/VLC 依赖它交付帧）")
}
