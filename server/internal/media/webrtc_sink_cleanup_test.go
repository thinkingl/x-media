package media

import (
	"errors"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// TestWebRTCSinkWriteFailureRemovesClient 复现写失败不清理：
// writeLoop 此前 WriteRTP 失败直接 return —— pion 本地写失败不必然触发
// 连接状态迁移，pair 滞留 clients map、队列无人消费、PeerConnection
// 资源延迟释放。
func TestWebRTCSinkWriteFailureRemovesClient(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_cleanup", Type: "webrtc"})
	require.NoError(t, err)

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	defer pc.Close()

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
		"video", "wr_cleanup")
	require.NoError(t, err)

	pair := sink.newWebRTCTrackPair(track, "test-peer")
	pair.writeFn = func(*rtp.Packet) error { return errors.New("simulated write failure") }
	pair.onClose = func() { sink.dropClient(pc) }
	sink.registerClient(pc, pair)

	sink.clientsMu.RLock()
	_, registered := sink.clients[pc]
	sink.clientsMu.RUnlock()
	require.True(t, registered)

	pair.enqueue(&rtp.Packet{})

	require.Eventually(t, func() bool {
		sink.clientsMu.RLock()
		defer sink.clientsMu.RUnlock()
		return len(sink.clients) == 0
	}, 2*time.Second, 10*time.Millisecond, "写失败后客户端必须被移除（旧代码滞留 map）")

	select {
	case <-pair.closeCh:
	default:
		t.Fatal("写失败后 pair 必须停止")
	}
}
