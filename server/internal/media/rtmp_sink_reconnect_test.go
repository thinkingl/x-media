package media

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newRTMPReconnectSink 构造指向 addr 的 RTMPSink 并 Start（复用 flvTestStreams 配置）。
func newRTMPReconnectSink(t *testing.T, addr string) *RTMPSink {
	t.Helper()
	sink, err := NewRTMPSink(&OutputConfig{
		ID:   "rtmp_" + t.Name(),
		Type: "rtmp",
		URL:  "rtmp://" + addr + "/live/" + t.Name(),
	})
	require.NoError(t, err)
	require.NoError(t, sink.Configure(flvTestStreams()))
	require.NoError(t, sink.Start(context.Background()))
	return sink
}

func rtmpTestFrame(pts int64) *Frame {
	return testFrame(0, FrameTypeVideo, CodecH264, pts,
		[]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04})
}

// TestRTMPSinkReconnectsAfterServerRecovery 复现"首连失败即永久死亡"：
// 旧代码连接失败后置 status=Error，此后所有 WriteFrame 早退，
// 即使服务端恢复也永远不再重试。
func TestRTMPSinkReconnectsAfterServerRecovery(t *testing.T) {
	// 先用一个必定拒绝连接的地址（服务未起）
	deadAddr := "127.0.0.1:1"
	sink := newRTMPReconnectSink(t, deadAddr)
	defer sink.Stop()

	// 首帧：连接失败（错误可接受）
	_ = sink.WriteFrame(rtmpTestFrame(0))

	// 服务端恢复：换成正常 mock 地址
	srv := startRTMPMockServer(t)
	defer srv.close()
	sink.mu.Lock()
	sink.addr = srv.addr()
	sink.mu.Unlock()

	// 退避窗口过后再次尝试（旧代码因 status=Error 永不重试）
	time.Sleep(3200 * time.Millisecond)
	for i := 0; i < 5; i++ {
		_ = sink.WriteFrame(rtmpTestFrame(int64(i+1) * 3000))
	}

	require.Eventually(t, func() bool {
		return len(srv.messages()) > 0
	}, 3*time.Second, 50*time.Millisecond,
		"服务端恢复后 RTMP sink 必须能重新连接并发送数据（旧代码置 Error 后永不重试）")
}

// TestRTMPSinkConnectTimeout 复现无超时挂死：
// 服务端 accept 后完全不回包，旧代码 Connect 在 ioReadFull 上永久阻塞，
// WriteFrame 持锁 → Stop/关停整体挂死。
func TestRTMPSinkConnectTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	// accept 但不读不写
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn // 保持连接打开，静默
		}
	}()

	// 注意：这里不 defer sink.Stop() —— 旧代码下 WriteFrame 持锁挂死时，
	// Stop 会阻塞在同一把锁上，把"测试失败"变成"测试悬挂"。
	sink := newRTMPReconnectSink(t, ln.Addr().String())

	done := make(chan error, 1)
	go func() { done <- sink.WriteFrame(rtmpTestFrame(0)) }()

	select {
	case <-done:
		// 新代码：连接步骤超时后返回错误（不挂死）
	case <-time.After(8 * time.Second):
		t.Fatal("WriteFrame 对不响应的服务端必须在超时后返回（旧代码永久挂死，且持锁阻塞关停）")
	}
}

// TestRTMPSinkStopThenStartRecovers 复现 Stop 后不可恢复：
// 旧代码 Stop 关闭 client（closed 永久）且不重置 started，
// Stop→Start 后每帧 "rtmp not connected"，输出静默失效。
func TestRTMPSinkStopThenStartRecovers(t *testing.T) {
	srv := startRTMPMockServer(t)
	defer srv.close()
	sink := newRTMPReconnectSink(t, srv.addr())
	defer sink.Stop()

	// 首次连上并发送
	require.Eventually(t, func() bool {
		_ = sink.WriteFrame(rtmpTestFrame(0))
		return len(srv.messages()) > 0
	}, 3*time.Second, 50*time.Millisecond, "首次应能连上并发送")

	// 等 mock 消息计数稳定（handleConn 异步记录，避免把首阶段的缓冲
	// 消息误判为 Stop→Start 后新发送的），再采样基线。
	stable := len(srv.messages())
	require.Eventually(t, func() bool {
		n := len(srv.messages())
		if n == stable {
			return true
		}
		stable = n
		return false
	}, 2*time.Second, 200*time.Millisecond, "mock 消息计数应稳定")
	before := len(srv.messages())

	// Stop → Start
	require.NoError(t, sink.Stop())
	require.NoError(t, sink.Start(context.Background()))
	require.Eventually(t, func() bool {
		_ = sink.WriteFrame(rtmpTestFrame(3000))
		return len(srv.messages()) > before
	}, 3*time.Second, 50*time.Millisecond,
		"Stop→Start 后必须能重新连接并发送（旧代码 client closed 永久且 started 未重置）")
}
