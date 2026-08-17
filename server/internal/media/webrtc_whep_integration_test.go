package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebRTCSink_WHEPIntegration 端到端：WebRTC sink + WHEP 信令，pion 客户端拉流
// 验证能建立 PeerConnection 并收到 RTP 视频帧。
func TestWebRTCSink_WHEPIntegration(t *testing.T) {
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_whep_it", Type: "webrtc"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))

	// 模拟 WHEP 信令端点
	srv := httptest.NewServer(http.HandlerFunc(sink.ServeWHEP))
	defer srv.Close()

	// pion 客户端：创建 PeerConnection，接收视频 track
	var received int64
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	defer pc.Close()

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	require.NoError(t, err)

	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			if pkt != nil && len(pkt.Payload) > 0 {
				atomic.AddInt64(&received, 1)
			}
		}
	})

	// 创建 offer 并通过 WHEP 发送
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))

	resp, err := http.Post(srv.URL, "application/sdp",
		bytes.NewReader([]byte(offer.SDP)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotEmpty(t, body)

	// 设置远端 answer
	err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(body),
	})
	require.NoError(t, err)

	// 等连接建立
	time.Sleep(2 * time.Second)

	// 推几帧到 sink
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	for i := 0; i < 20; i++ {
		f := testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr)
		f.Header.Flags = FlagKeyframe
		require.NoError(t, sink.WriteFrame(f))
	}

	// 等接收
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&received) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("received RTP packets: %d", atomic.LoadInt64(&received))
	assert.Greater(t, atomic.LoadInt64(&received), int64(0), "should receive RTP video packets")
}
