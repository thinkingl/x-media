package media

// gb28181_m4_test.go —— M4 健壮性与兼容开关测试（方案 §5.9/§9）：
// TCP 媒体（$-帧，两种 setup 方向）、tcp_role 强制、rtp_mtu、平台会话客户端、
// 心跳静默→自动重注册。

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startGB28181SyntheticWriter 以 25fps 视频 + AAC 随行的节奏持续写帧，返回停止函数。
func startGB28181SyntheticWriter(sink *GB28181Sink) func() {
	stop := make(chan struct{})
	go func() {
		var videoPTS, audioPTS int64
		page := byte(0x30)
		for {
			select {
			case <-stop:
				return
			default:
			}
			page++
			key := videoPTS == 0 || page%25 == 0
			_ = sink.WriteFrame(videoFrame(videoPTS, h264AnnexB(key, page), key))
			videoPTS += 3600
			_ = sink.WriteFrame(audioFrame(audioPTS, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02}))
			audioPTS += 1024
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return func() { close(stop) }
}

// TestGB28181Sink_TCPPlatformListens 平台监听、设备主动连（offer setup:passive →
// answer setup:active），媒体走 $-帧 TCP 流。同时断言 KDM 类平台的匹配契约：
// 媒体连接源端口必须等于应答 SDP 公告的 m= 端口（§5.9 实测）。
func TestGB28181Sink_TCPPlatformListens(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x22334455)
	advPort := platform.inviteTCPPlatformListens(t, ssrc, "passive", "active")
	stop := startGB28181SyntheticWriter(sink)
	defer stop()

	pkts := platform.waitRTP(t, 30, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	ps := assemblePS(pkts)
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xBA}), "PS pack header（TCP 路径）")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x65}), "IDR（TCP 路径）")

	// KDM 匹配契约：连接源端口 == 应答 SDP 公告端口（bind-before-connect）。
	require.Eventually(t, func() bool { return platform.tcpAcceptedRemoteAddr() != "" },
		3*time.Second, 20*time.Millisecond)
	_, remotePort, err := net.SplitHostPort(platform.tcpAcceptedRemoteAddr())
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(advPort), remotePort,
		"TCP 媒体连接必须从应答 SDP 公告端口发出（KDM 按 c=/m= 匹配发送端，§5.9）")
}

// TestGB28181Sink_TCPAnswerAdvertisesNATExternalIP NAT 场景（学到外部地址后）：
// 应答 SDP 的 c= 必须用外部地址，且连接必须从公告端口发出（KDM 匹配契约）。
func TestGB28181Sink_TCPAnswerAdvertisesNATExternalIP(t *testing.T) {
	platform := newFakeSIPPlatform(t, gbTestPlatformID, gbTestRealm, gbTestDeviceID, gbTestPassword)
	platform.setNAT("203.0.113.9", 45678)
	sink := startGB28181SinkOnPlatform(t, platform)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x66778899)
	advPort := platform.inviteTCPPlatformListens(t, ssrc, "passive", "active")

	answer := platform.inviteAnswer()
	assert.Contains(t, answer, "c=IN IP4 203.0.113.9",
		"NAT 场景应答 SDP 应使用学到的外部 IP（KDM 按 c=/m= 登记预期发送端，§5.9）")

	stop := startGB28181SyntheticWriter(sink)
	defer stop()
	pkts := platform.waitRTP(t, 20, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)

	require.Eventually(t, func() bool { return platform.tcpAcceptedRemoteAddr() != "" },
		3*time.Second, 20*time.Millisecond)
	_, remotePort, err := net.SplitHostPort(platform.tcpAcceptedRemoteAddr())
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(advPort), remotePort,
		"NAT 场景连接源端口仍必须等于应答 SDP 公告端口")
}

// TestGB28181Sink_TCPDeviceListens 平台主动拨号设备（offer setup:active →
// answer setup:passive：设备监听，answer 携带设备端口）。
func TestGB28181Sink_TCPDeviceListens(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x33445566)
	platform.inviteTCPDeviceListens(t, ssrc)
	stop := startGB28181SyntheticWriter(sink)
	defer stop()

	pkts := platform.waitRTP(t, 30, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	ps := assemblePS(pkts)
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x65}), "IDR（设备被动监听路径）")
}

// TestGB28181Sink_TCPRoleOverride 异常平台 setup 语义相反时，tcp_role=active
// 强制设备主动连（即使 offer 标 active 本应让设备被动）。
func TestGB28181Sink_TCPRoleOverride(t *testing.T) {
	sink, platform := startGB28181StreamSink(t, func(c *GB28181Config) {
		c.TCPRole = "active"
	})
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x44556677)
	// offer 标 active（RFC 语义下设备应被动），但设备被强制 active → 平台监听收流
	platform.inviteTCPPlatformListens(t, ssrc, "active", "active")
	stop := startGB28181SyntheticWriter(sink)
	defer stop()

	pkts := platform.waitRTP(t, 20, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	assert.True(t, bytes.Contains(assemblePS(pkts), []byte{0x00, 0x00, 0x01, 0xE0}), "视频 PES")
}

// TestGB28181Sink_RTPMTU rtp_mtu=1200：所有 RTP 负载 ≤1200（老旧平台/隧道场景）。
func TestGB28181Sink_RTPMTU(t *testing.T) {
	sink, platform := startGB28181StreamSink(t, func(c *GB28181Config) {
		c.RTPMTU = 1200
	})
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x55667788)
	platform.invite(t, ssrc)
	stop := startGB28181SyntheticWriter(sink)
	defer stop()

	pkts := platform.waitRTP(t, 30, 5*time.Second)
	maxLen := 0
	for i := range pkts {
		if len(pkts[i].Payload) > maxLen {
			maxLen = len(pkts[i].Payload)
		}
	}
	assert.LessOrEqual(t, maxLen, 1200, "RTP 负载应遵守 rtp_mtu=1200")
	assertRTPBasics(t, pkts, ssrc)
}

// TestGB28181Sink_Clients 平台会话客户端展示（ClientInfoProvider）：
// 点播期间 1 个会话（含 SSRC/传输/地址），BYE 后清空。
func TestGB28181Sink_Clients(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))
	require.Empty(t, sink.Clients(), "未点播时无会话")

	const ssrc = uint32(0x66778899)
	platform.invite(t, ssrc)

	require.Eventually(t, func() bool { return len(sink.Clients()) == 1 }, 3*time.Second, 50*time.Millisecond)
	c := sink.Clients()[0]
	assert.Equal(t, "udp", c.Transport)
	assert.NotEmpty(t, c.Address, "应展示平台 RTP 目的地址")
	assert.Contains(t, c.UserAgent, "GB28181")
	assert.Contains(t, c.UserAgent, "ssrc=")
	assert.False(t, c.ConnectedAt.IsZero())

	platform.bye(t)
	require.Eventually(t, func() bool { return len(sink.Clients()) == 0 }, 3*time.Second, 50*time.Millisecond)
}

// startGB28181SinkOnPlatform 在既有 fake 平台上启动 sink（NAT/覆盖类测试用）。
func startGB28181SinkOnPlatform(t *testing.T, platform *fakeSIPPlatform, extra ...func(*GB28181Config)) *GB28181Sink {
	t.Helper()
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
	}
	for _, f := range extra {
		if f != nil {
			f(cfg)
		}
	}
	sink, err := NewGB28181Sink(&OutputConfig{ID: "gb28181_m4_test", Type: "gb28181", GB28181: cfg})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	t.Cleanup(func() { _ = sink.Stop() })
	platform.waitEvent(t, "registered", 5*time.Second)
	return sink
}

// TestGB28181Sink_NATContactLearning：平台响应回显 received/rport 后，
// 设备应学习外部地址并在后续 Contact 中使用（NAT 穿越，§5.9）。
func TestGB28181Sink_NATContactLearning(t *testing.T) {
	platform := newFakeSIPPlatform(t, gbTestPlatformID, gbTestRealm, gbTestDeviceID, gbTestPassword)
	platform.setNAT("203.0.113.9", 45678)
	_ = startGB28181SinkOnPlatform(t, platform)

	contacts := platform.contactsOf("REGISTER")
	require.GreaterOrEqual(t, len(contacts), 2, "应有未认证+已认证两次 REGISTER")
	assert.NotContains(t, contacts[0], "203.0.113.9", "首包 REGISTER 尚不可知外部地址")
	assert.Contains(t, contacts[len(contacts)-1], "203.0.113.9:45678",
		"学到外部地址后 Contact 应改用 NAT 映射地址")
}

// TestGB28181Sink_LocalIPOverride：local_ip 显式覆盖 Contact 地址（固定映射场景）。
func TestGB28181Sink_LocalIPOverride(t *testing.T) {
	platform := newFakeSIPPlatform(t, gbTestPlatformID, gbTestRealm, gbTestDeviceID, gbTestPassword)
	_ = startGB28181SinkOnPlatform(t, platform, func(c *GB28181Config) {
		c.LocalIP = "198.51.100.7"
	})
	contacts := platform.contactsOf("REGISTER")
	require.NotEmpty(t, contacts)
	assert.Contains(t, contacts[0], "198.51.100.7", "local_ip 覆盖应体现在首个 REGISTER")
}

// TestGB28181Sink_SilentPlatformReregister 心跳静默 → 静默检测触发自动重注册。
func TestGB28181Sink_SilentPlatformReregister(t *testing.T) {
	_, platform := startGB28181StreamSink(t, func(c *GB28181Config) {
		c.HeartbeatInterval = 1
		c.HeartbeatTimeoutCount = 2
	})
	require.Equal(t, 1, platform.regCount(), "首次注册完成")

	platform.setSilent(true)
	defer platform.setSilent(false)

	// 2 个心跳周期无应答 → 触发重注册（REGISTER 仍被应答，立即完成）
	require.Eventually(t, func() bool { return platform.regCount() >= 2 }, 10*time.Second, 200*time.Millisecond,
		"静默后应自动重注册")
}
