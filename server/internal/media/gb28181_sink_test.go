package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gb28181TestConfig() *GB28181Config {
	return &GB28181Config{
		PlatformIP:   "192.168.88.100",
		PlatformPort: 15060,
		DeviceID:     "34020000001320000001",
		SIPDomain:    "3402000000",
		Password:     "12345678",
	}
}

func TestNewGB28181Sink_配置校验与默认值(t *testing.T) {
	t.Run("缺少 GB28181 配置段", func(t *testing.T) {
		_, err := NewGB28181Sink(&OutputConfig{ID: "x", Type: "gb28181"})
		require.Error(t, err)
	})

	t.Run("device_id 非 20 位", func(t *testing.T) {
		cfg := gb28181TestConfig()
		cfg.DeviceID = "123"
		_, err := NewGB28181Sink(&OutputConfig{ID: "x", Type: "gb28181", GB28181: cfg})
		require.Error(t, err)
	})

	t.Run("平台地址必填", func(t *testing.T) {
		cfg := gb28181TestConfig()
		cfg.PlatformIP = ""
		_, err := NewGB28181Sink(&OutputConfig{ID: "x", Type: "gb28181", GB28181: cfg})
		require.Error(t, err)
	})

	t.Run("默认值", func(t *testing.T) {
		sink, err := NewGB28181Sink(&OutputConfig{ID: "gb_defaults", Type: "gb28181", GB28181: gb28181TestConfig()})
		require.NoError(t, err)
		assert.Equal(t, "34020000001320000001", sink.cfg.ChannelID, "channel_id 缺省应等于 device_id")
		assert.Equal(t, 3600, sink.cfg.RegisterInterval)
		assert.Equal(t, 60, sink.cfg.HeartbeatInterval)
		assert.Equal(t, 3, sink.cfg.HeartbeatTimeoutCount)
		assert.Equal(t, 1400, sink.cfg.RTPMTU)
		assert.Equal(t, "auto", sink.cfg.VideoCodec)
		assert.Equal(t, "udp", sink.cfg.Transport)
	})

	t.Run("枚举非法值", func(t *testing.T) {
		cfg := gb28181TestConfig()
		cfg.Transport = "sctp"
		_, err := NewGB28181Sink(&OutputConfig{ID: "x", Type: "gb28181", GB28181: cfg})
		require.Error(t, err)

		cfg = gb28181TestConfig()
		cfg.VideoCodec = "mpeg4"
		_, err = NewGB28181Sink(&OutputConfig{ID: "x", Type: "gb28181", GB28181: cfg})
		require.Error(t, err)
	})
}

func TestGB28181Sink_Configure编码校验(t *testing.T) {
	videoH264 := StreamInfo{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000}
	videoH265 := StreamInfo{ChannelID: 0, Kind: "video", CodecID: CodecH265, CodecName: "H265", ClockRate: 90000}
	audioAAC := StreamInfo{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 44100}

	t.Run("H264+音频 通过", func(t *testing.T) {
		sink, err := NewGB28181Sink(&OutputConfig{ID: "gb_cfg1", Type: "gb28181", GB28181: gb28181TestConfig()})
		require.NoError(t, err)
		require.NoError(t, sink.Configure([]StreamInfo{videoH264, audioAAC}))
	})

	t.Run("H265 auto 通过", func(t *testing.T) {
		sink, err := NewGB28181Sink(&OutputConfig{ID: "gb_cfg2", Type: "gb28181", GB28181: gb28181TestConfig()})
		require.NoError(t, err)
		require.NoError(t, sink.Configure([]StreamInfo{videoH265}))
	})

	t.Run("H265 + video_codec=h264 拒绝", func(t *testing.T) {
		cfg := gb28181TestConfig()
		cfg.VideoCodec = "h264"
		sink, err := NewGB28181Sink(&OutputConfig{ID: "gb_cfg3", Type: "gb28181", GB28181: cfg})
		require.NoError(t, err)
		err = sink.Configure([]StreamInfo{videoH265})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "H.265")
	})

	t.Run("无视频轨道 拒绝", func(t *testing.T) {
		sink, err := NewGB28181Sink(&OutputConfig{ID: "gb_cfg4", Type: "gb28181", GB28181: gb28181TestConfig()})
		require.NoError(t, err)
		require.Error(t, sink.Configure([]StreamInfo{audioAAC}))
	})
}
