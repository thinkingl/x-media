package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/x-media/x-media-server/internal/model"
)

// TestBuildMediaOutputConfig_GB28181 验证 DB 配置 → 媒体引擎配置的映射与默认值：
// audio_enable/gop_replay 缺省 true；tele_boot_restart/control_reject_unimplemented
// 缺省 false；其余字段逐项透传（§7.1 存储链路）。
func TestBuildMediaOutputConfig_GB28181(t *testing.T) {
	audioOff := false
	rejectUnimpl := true
	c := &model.OutputConfig{
		PlatformIP:                 "192.168.88.100",
		PlatformPort:               15060,
		PlatformID:                 "34020000002000000001",
		DeviceID:                   "34020000001320000001",
		ChannelID:                  "34020000001310000001",
		SIPDomain:                  "3402000000",
		Password:                   "12345678",
		Transport:                  "tcp-out",
		TCPRole:                    "active",
		RTPMTU:                     1200,
		VideoCodec:                 "h264",
		LocalSIPPort:               15060,
		RegisterInterval:           1800,
		HeartbeatInterval:          30,
		HeartbeatTimeoutCount:      3,
		ProtocolVersion:            "3.0",
		AudioEnable:                &audioOff,
		DeviceName:                 "cam",
		Manufacturer:               "x-media",
		DeviceModel:                "virtual",
		Firmware:                   "1.0.0",
		ControlRejectUnimplemented: &rejectUnimpl,
	}
	moc := buildMediaOutputConfig("out-1", model.OutputTypeGB28181, c)

	require.NotNil(t, moc.GB28181)
	g := moc.GB28181
	assert.Equal(t, "192.168.88.100", g.PlatformIP)
	assert.Equal(t, 15060, g.PlatformPort)
	assert.Equal(t, "34020000002000000001", g.PlatformID)
	assert.Equal(t, "34020000001320000001", g.DeviceID)
	assert.Equal(t, "34020000001310000001", g.ChannelID)
	assert.Equal(t, "tcp-out", g.Transport)
	assert.Equal(t, "active", g.TCPRole)
	assert.Equal(t, 1200, g.RTPMTU)
	assert.Equal(t, "h264", g.VideoCodec)
	assert.Equal(t, 1800, g.RegisterInterval)
	assert.Equal(t, 30, g.HeartbeatInterval)
	assert.Equal(t, "3.0", g.ProtocolVersion)
	assert.False(t, g.AudioEnable, "显式 audio_enable=false 应透传")
	assert.True(t, g.GOPReplay, "gop_replay 缺省 true")
	assert.False(t, g.TeleBootRestart, "tele_boot_restart 缺省 false（默认拒绝）")
	assert.True(t, g.ControlRejectUnimplemented)
	assert.Equal(t, "cam", g.DeviceName)

	// 非 GB28181 类型不产生 GB28181 段
	moc2 := buildMediaOutputConfig("out-2", model.OutputTypeRTMP, c)
	assert.Nil(t, moc2.GB28181)
}
