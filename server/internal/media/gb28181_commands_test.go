package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gbTestRealm      = "3402000000"
	gbTestDeviceID   = "34020000001320000001"
	gbTestChannelID  = "34020000001310000001"
	gbTestPlatformID = "34020000002000000001"
	gbTestPassword   = "12345678"
)

// startGB28181CommandTestSink 启动一个注册到 FakePlatform 的 sink（指令矩阵测试用）。
func startGB28181CommandTestSink(t *testing.T, extra func(*GB28181Config)) (*GB28181Sink, *fakeSIPPlatform) {
	t.Helper()
	platform := newFakeSIPPlatform(t, gbTestPlatformID, gbTestRealm, gbTestDeviceID, gbTestPassword)
	ip, port := platform.addr()

	cfg := &GB28181Config{
		PlatformIP:        ip,
		PlatformPort:      port,
		PlatformID:        gbTestPlatformID,
		DeviceID:          gbTestDeviceID,
		ChannelID:         gbTestChannelID,
		SIPDomain:         gbTestRealm,
		Password:          gbTestPassword,
		HeartbeatInterval: 60,
		DeviceName:        "X-Media 虚拟摄像头",
		Manufacturer:      "x-media",
		DeviceModel:       "gb28181-sink",
		Firmware:          "1.0.0",
	}
	if extra != nil {
		extra(cfg)
	}
	sink, err := NewGB28181Sink(&OutputConfig{ID: "gb28181_test_cmds", Type: "gb28181", GB28181: cfg})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	t.Cleanup(func() { _ = sink.Stop() })
	platform.waitEvent(t, "registered", 5*time.Second)
	return sink, platform
}

// TestGB28181Sink_CatalogAlarmChannel 报警通道（类型码 134）作为额外目录项上报：
// SumNum=2（视频通道 + 报警通道）、ParentID=设备编码、Status=ON。
func TestGB28181Sink_CatalogAlarmChannel(t *testing.T) {
	const alarmID = "34020000001340000001"
	_, platform := startGB28181CommandTestSink(t, func(c *GB28181Config) {
		c.AlarmChannelID = alarmID
	})

	ev := platform.query(t, `<Query><CmdType>Catalog</CmdType><SN>201</SN><DeviceID>`+gbTestDeviceID+`</DeviceID></Query>`)
	body := ev.msg.Body
	assert.Contains(t, body, "<SumNum>2</SumNum>", "目录应含视频通道+报警通道")
	assert.Contains(t, body, "<DeviceID>"+gbTestChannelID+"</DeviceID>", "视频通道仍在")
	assert.Contains(t, body, "<DeviceID>"+alarmID+"</DeviceID>", "目录应包含报警通道")
	assert.Contains(t, body, "<ParentID>"+gbTestDeviceID+"</ParentID>", "报警通道父节点=设备编码")
	assert.Contains(t, body, "<Status>ON</Status>")
}

// TestGB28181Sink_CommandMatrix 指令应答矩阵（方案 §5.3/§5.4）：
// 平台下发 目录/设备信息/状态/录像查询/配置下载/控制/订阅/OPTIONS，
// 断言设备侧 MANSCDP/SIP 应答内容。
func TestGB28181Sink_CommandMatrix(t *testing.T) {
	_, platform := startGB28181CommandTestSink(t, nil)

	t.Run("Catalog 目录查询", func(t *testing.T) {
		ev := platform.query(t, `<Query><CmdType>Catalog</CmdType><SN>101</SN><DeviceID>`+gbTestDeviceID+`</DeviceID></Query>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>Catalog</CmdType>")
		assert.Contains(t, body, "<SN>101</SN>")
		assert.Contains(t, body, "<DeviceID>"+gbTestDeviceID+"</DeviceID>")
		assert.Contains(t, body, "<SumNum>1</SumNum>")
		assert.Contains(t, body, "<DeviceID>"+gbTestChannelID+"</DeviceID>", "目录应包含配置的通道编码")
		assert.Contains(t, body, "X-Media 虚拟摄像头")
		assert.Contains(t, body, "<Manufacturer>x-media</Manufacturer>")
		assert.Contains(t, body, "<Status>ON</Status>")
	})

	t.Run("DeviceInfo 设备信息", func(t *testing.T) {
		ev := platform.query(t, `<Query><CmdType>DeviceInfo</CmdType><SN>102</SN><DeviceID>`+gbTestDeviceID+`</DeviceID></Query>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>DeviceInfo</CmdType>")
		assert.Contains(t, body, "<Manufacturer>x-media</Manufacturer>")
		assert.Contains(t, body, "<Model>gb28181-sink</Model>")
		assert.Contains(t, body, "<Firmware>1.0.0</Firmware>")
	})

	t.Run("DeviceStatus 设备状态", func(t *testing.T) {
		ev := platform.query(t, `<Query><CmdType>DeviceStatus</CmdType><SN>103</SN><DeviceID>`+gbTestDeviceID+`</DeviceID></Query>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>DeviceStatus</CmdType>")
		assert.Contains(t, body, "<Online>ONLINE</Online>")
		assert.Contains(t, body, "<Status>OK</Status>")
	})

	t.Run("RecordInfo 录像查询（空列表）", func(t *testing.T) {
		ev := platform.query(t, `<Query><CmdType>RecordInfo</CmdType><SN>104</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
			`<StartTime>2026-10-15T00:00:00</StartTime><EndTime>2026-10-15T23:59:59</EndTime></Query>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>RecordInfo</CmdType>")
		assert.Contains(t, body, "<SumNum>0</SumNum>")
		assert.Contains(t, body, `Num="0"`)
	})

	t.Run("ConfigDownload 配置下载（BasicParam）", func(t *testing.T) {
		ev := platform.query(t, `<Query><CmdType>ConfigDownload</CmdType><SN>105</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
			`<ConfigType>BasicParam</ConfigType></Query>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>ConfigDownload</CmdType>")
		assert.Contains(t, body, "<Result>OK</Result>")
		assert.Contains(t, body, "<BasicParam>")
		assert.Contains(t, body, "<Expiration>3600</Expiration>")
		assert.Contains(t, body, "<HeartBeatInterval>60</HeartBeatInterval>")
	})

	t.Run("DeviceControl IFrame（执行→Result OK）", func(t *testing.T) {
		ev := platform.query(t, `<Control><CmdType>DeviceControl</CmdType><SN>106</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
			`<IFrameCmd>Send</IFrameCmd></Control>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>DeviceControl</CmdType>")
		assert.Contains(t, body, "<Result>OK</Result>")
	})

	t.Run("DeviceControl PTZ（执行→Result OK）", func(t *testing.T) {
		// 8 字节 PTZ 位串：A5 0F 01 20 00 00 00 + 校验和（前 7 字节之和 & 0xFF = D5）
		ev := platform.query(t, `<Control><CmdType>DeviceControl</CmdType><SN>107</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
			`<PTZCmd>A50F0120000000D5</PTZCmd></Control>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>DeviceControl</CmdType>")
		assert.Contains(t, body, "<Result>OK</Result>")
	})

	t.Run("DeviceControl TeleBoot（默认拒绝→Result ERROR）", func(t *testing.T) {
		ev := platform.query(t, `<Control><CmdType>DeviceControl</CmdType><SN>108</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
			`<TeleBoot>Boot</TeleBoot></Control>`)
		body := ev.msg.Body
		assert.Contains(t, body, "<CmdType>DeviceControl</CmdType>")
		assert.Contains(t, body, "<Result>ERROR</Result>")
	})

	t.Run("SUBSCRIBE 目录订阅（200 + Expires 回显）", func(t *testing.T) {
		platform.sendRequest(t, "SUBSCRIBE", "Event: Catalog\r\nExpires: 3600\r\n", "")
		resp := platform.waitMethodResponse(t, "SUBSCRIBE", 5*time.Second)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "3600", resp.Expires)
	})

	t.Run("OPTIONS 探活（200）", func(t *testing.T) {
		platform.sendRequest(t, "OPTIONS", "", "")
		resp := platform.waitMethodResponse(t, "OPTIONS", 5*time.Second)
		assert.Equal(t, 200, resp.StatusCode)
	})
}

// TestGB28181Sink_TeleBootRestart 配置开放 TeleBoot 后应接受（Result OK）。
func TestGB28181Sink_TeleBootRestart(t *testing.T) {
	_, platform := startGB28181CommandTestSink(t, func(c *GB28181Config) {
		c.TeleBootRestart = true
	})
	ev := platform.query(t, `<Control><CmdType>DeviceControl</CmdType><SN>201</SN><DeviceID>`+gbTestDeviceID+`</DeviceID>`+
		`<TeleBoot>Boot</TeleBoot></Control>`)
	assert.Contains(t, ev.msg.Body, "<Result>OK</Result>")
}
