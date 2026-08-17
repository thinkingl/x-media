package media

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebRTCSink_ConfigureH264(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_test", Type: "webrtc"})
	require.NoError(t, err)
	defer sink.Stop()
	require.NoError(t, sink.Start(context.Background()))

	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))
	assert.True(t, sink.ready)
	assert.NotNil(t, sink.h264Enc)
}

func TestWebRTCSink_ConfigureRejectsH265(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_h265", Type: "webrtc"})
	require.NoError(t, err)
	defer sink.Stop()
	require.NoError(t, sink.Start(context.Background()))

	hevcCfg := loadHevcConfigFromFile(t, testFixturePath(t, "../../../test_data/h265_test.mp4"))
	err = sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH265, CodecName: "H265", ClockRate: 90000, CodecConfig: hevcCfg},
	})
	assert.Error(t, err, "WebRTC sink should reject H265 source")
}

func TestWebRTCSink_ConfigureRequiresVideo(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_novid", Type: "webrtc"})
	require.NoError(t, err)
	defer sink.Stop()
	require.NoError(t, sink.Start(context.Background()))

	err = sink.Configure([]StreamInfo{
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC"},
	})
	assert.Error(t, err, "WebRTC sink requires video stream")
}

func TestWebRTCSink_ClientsEmpty(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_clients", Type: "webrtc"})
	require.NoError(t, err)
	defer sink.Stop()
	require.NoError(t, sink.Start(context.Background()))
	require.Nil(t, sink.Clients())
}

// TestWebRTCSink_WriteFrameBeforeReady 验证未 Configure 时 WriteFrame 安全返回。
func TestWebRTCSink_WriteFrameBeforeReady(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_noready", Type: "webrtc"})
	require.NoError(t, err)
	defer sink.Stop()
	require.NoError(t, sink.Start(context.Background()))

	f := testFrame(0, FrameTypeVideo, CodecH264, 3000, []byte{0x00, 0x00, 0x00, 0x01, 0x65})
	f.Header.Flags = FlagKeyframe
	require.NoError(t, sink.WriteFrame(f))
}
