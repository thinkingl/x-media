package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRTSPInputRestartDuringReconnectDoesNotDuplicateSession 复现断流重连
// 期间再次 Start 产生第二个 readLoop：两个循环共享 r.client，恢复后连出
// 两条 RTSP 会话、帧重复分发。
func TestRTSPInputRestartDuringReconnectDoesNotDuplicateSession(t *testing.T) {
	globalRTSPManager.StopAll()
	defer globalRTSPManager.StopAll()

	addr := "127.0.0.1:" + freeTCPPort(t)

	var stopFeed1 chan struct{}
	sink1 := startRTSPTestServer(t, addr, &stopFeed1)
	defer sink1.Stop()

	input, err := NewRTSPInput(&InputConfig{
		ID:        "rtsp_restart_" + t.Name(),
		Type:      "rtsp",
		URL:       "rtsp://" + addr + "/live/" + sink1.ID(),
		Transport: "tcp",
	})
	require.NoError(t, err)
	recvSink := NewMockSink("rtsp_restart_recv")
	recvPipe := NewDefaultPipe(2048)
	require.NoError(t, recvPipe.Bind(input, recvSink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, input.Start(ctx))
	require.NoError(t, recvPipe.Start(ctx))
	defer recvPipe.Stop()
	defer input.Stop()

	waitCond(t, 10*time.Second, func() bool {
		return input.Status() == StreamStatusRunning
	}, "input running")
	waitCond(t, 10*time.Second, func() bool {
		return len(recvSink.Frames()) > 0
	}, "frames received")

	// 断流 → 重连循环中（status=Error）
	close(stopFeed1)
	globalRTSPManager.StopAll()
	waitCond(t, 15*time.Second, func() bool {
		return input.Status() == StreamStatusError
	}, "input detects disconnect")

	// 同地址重启 server，然后在重连期间再次 Start
	var stopFeed2 chan struct{}
	sink2 := startRTSPTestServer(t, addr, &stopFeed2)
	defer func() {
		close(stopFeed2)
		sink2.Stop()
	}()
	require.NoError(t, input.Start(ctx), "重连期间的 Start 不应报错（旧代码会另起 readLoop）")

	waitCond(t, 30*time.Second, func() bool {
		return input.Status() == StreamStatusRunning
	}, "input reconnects")
	waitCond(t, 15*time.Second, func() bool {
		return len(sink2.Clients()) > 0
	}, "server sees reader")

	// 给潜在的第二条会话留出建立时间（须超过重连退避 3s），再断言只有一条
	time.Sleep(5 * time.Second)
	assert.Equal(t, 1, len(sink2.Clients()),
		"恢复后必须只有一条 RTSP 会话（重复 Start 不得产生第二个 readLoop）")
}
