package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/x-media/x-media-server/internal/model"
)

const gb28181ValidConfig = `{
  "platform_ip": "192.168.88.100",
  "platform_port": 15060,
  "platform_id": "34020000002000000001",
  "device_id": "34020000001320000001",
  "sip_domain": "3402000000",
  "password": "12345678"
}`

func TestValidateOutputConfig_GB28181(t *testing.T) {
	t.Run("合法配置通过", func(t *testing.T) {
		require.NoError(t, validateOutputConfig(model.OutputTypeGB28181, gb28181ValidConfig))
	})

	t.Run("channel_id 可省略（缺省=device_id）", func(t *testing.T) {
		cfg := `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x"}`
		require.NoError(t, validateOutputConfig(model.OutputTypeGB28181, cfg))
	})

	bad := []struct {
		name string
		json string
	}{
		{"平台地址缺失", `{"platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x"}`},
		{"端口越界", `{"platform_ip":"1.2.3.4","platform_port":70000,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x"}`},
		{"device_id 非 20 位", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"123","sip_domain":"3402000000","password":"x"}`},
		{"device_id 含非数字", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"3402000000132000000a","sip_domain":"3402000000","password":"x"}`},
		{"platform_id 非 20 位", `{"platform_ip":"1.2.3.4","platform_port":5060,"platform_id":"123","device_id":"34020000001320000001","sip_domain":"3402000000","password":"x"}`},
		{"sip_domain 缺失", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","password":"x"}`},
		{"密码缺失", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000"}`},
		{"transport 非法", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x","transport":"udp-multicast"}`},
		{"video_codec 非法", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x","video_codec":"h266"}`},
		{"rtp_mtu 越界", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x","rtp_mtu":2000}`},
		{"心跳周期越界", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x","heartbeat_interval":5000}`},
		{"心跳超时次数越界", `{"platform_ip":"1.2.3.4","platform_port":5060,"device_id":"34020000001320000001","sip_domain":"3402000000","password":"x","heartbeat_timeout_count":11}`},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			assert.Error(t, validateOutputConfig(model.OutputTypeGB28181, c.json))
		})
	}
}

func TestIsValidOutputType_GB28181(t *testing.T) {
	assert.True(t, isValidOutputType(model.OutputTypeGB28181))
}
