package media

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRTSPSinkStopReleasesServer 复现 RTSP server 永不关闭：
// StopAll 仅测试调用，生产中输出删除/换地址后 TCP 监听与 UDP 端口永久累积。
func TestRTSPSinkStopReleasesServer(t *testing.T) {
	globalRTSPManager.StopAll()
	defer globalRTSPManager.StopAll()

	addr := "127.0.0.1:" + freeTCPPort(t)
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "rtsp_release_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: addr,
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	require.NoError(t, sink.Configure(flvTestStreams()))

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err, "Start 后监听应可连接")
	_ = conn.Close()

	require.NoError(t, sink.Stop())

	require.Eventually(t, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return true
		}
		_ = c.Close()
		return false
	}, 3*time.Second, 50*time.Millisecond,
		"sink Stop 后 RTSP server 监听必须关闭（旧代码从不关闭）")
}

// TestRTSPServerSharedByRefs 验证同 addr 多 sink 共享 server 的引用计数：
// 任一 sink Stop 不得关闭仍被他人使用的 server，最后一个 Stop 才关闭。
func TestRTSPServerSharedByRefs(t *testing.T) {
	globalRTSPManager.StopAll()
	defer globalRTSPManager.StopAll()

	addr := "127.0.0.1:" + freeTCPPort(t)
	mk := func(id string) *RTSPSink {
		s, err := NewRTSPSink(&OutputConfig{ID: id, Type: "rtsp", Mode: "server", Addr: addr})
		require.NoError(t, err)
		require.NoError(t, s.Start(context.Background()))
		require.NoError(t, s.Configure(flvTestStreams()))
		return s
	}
	s1 := mk("rtsp_ref1_" + t.Name())
	s2 := mk("rtsp_ref2_" + t.Name())

	require.NoError(t, s1.Stop())
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err, "还有 sink 在用同一 server，Stop 其一不得关闭监听")
	_ = c.Close()

	require.NoError(t, s2.Stop())
	require.Eventually(t, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return true
		}
		_ = c.Close()
		return false
	}, 3*time.Second, 50*time.Millisecond,
		"最后一个 sink Stop 后监听必须关闭")
}
