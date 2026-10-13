package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startRTSPTestServer 在指定 addr 上起一个 RTSPSink server 并持续喂帧，
// 返回喂帧停止函数。复用 flvTestStreams（H264 SPS/PPS）与 MockSource 管线。
func startRTSPTestServer(t *testing.T, addr string, stopFeed *chan struct{}) *RTSPSink {
	t.Helper()
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "rtsp_reconnect_srv_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: addr,
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	require.NoError(t, sink.Configure(flvTestStreams()))

	src := NewMockSource("rtsp_reconnect_src_"+t.Name(), flvTestStreams())
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	feedStop := make(chan struct{})
	*stopFeed = feedStop
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		pts := int64(0)
		for {
			select {
			case <-feedStop:
				return
			case <-ticker.C:
				pts += 3000
				src.Push(testFrame(0, FrameTypeVideo, CodecH264, pts,
					[]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04}))
			}
		}
	}()
	return sink
}

// TestRTSPInputReconnectsAfterDisconnect 复现断流无检测/无重连：
// 旧代码 readLoop 在 Play 后只等 ctx.Done，远端断开后 status 永远
// running、无帧、不重试，用户无感知。
func TestRTSPInputReconnectsAfterDisconnect(t *testing.T) {
	globalRTSPManager.StopAll()
	defer globalRTSPManager.StopAll()

	addr := "127.0.0.1:" + freeTCPPort(t)

	// 第一段：server 在线，输入拉流正常
	var stopFeed1 chan struct{}
	sink1 := startRTSPTestServer(t, addr, &stopFeed1)
	defer sink1.Stop()

	input, err := NewRTSPInput(&InputConfig{
		ID:        "rtsp_reconnect_" + t.Name(),
		Type:      "rtsp",
		URL:       "rtsp://" + addr + "/live/" + sink1.ID(),
		Transport: "tcp",
	})
	require.NoError(t, err)
	recvSink := NewMockSink("rtsp_reconnect_recv")
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
	}, "RTSP input running")
	waitCond(t, 10*time.Second, func() bool {
		return len(recvSink.Frames()) > 0
	}, "frames received from server")

	// 第二段：整个 RTSP server 下线（listener 关闭）
	// 先停喂帧，再关 server —— 模拟远端断流
	close(stopFeed1)
	globalRTSPManager.StopAll()

	// 断流检测：status 必须进入 Error（旧代码永远 Running → 复现失败）
	waitCond(t, 15*time.Second, func() bool {
		return input.Status() == StreamStatusError
	}, "input detects disconnect (status=error)")

	// 第三段：同地址重启 server → 输入必须自动重连并恢复收帧
	var stopFeed2 chan struct{}
	sink2 := startRTSPTestServer(t, addr, &stopFeed2)
	defer func() {
		close(stopFeed2)
		sink2.Stop()
	}()

	waitCond(t, 30*time.Second, func() bool {
		return input.Status() == StreamStatusRunning
	}, "input reconnects after server restart")

	framesAfterReconnect := len(recvSink.Frames())
	waitCond(t, 15*time.Second, func() bool {
		return len(recvSink.Frames()) > framesAfterReconnect
	}, "frames flow again after reconnect")
}
