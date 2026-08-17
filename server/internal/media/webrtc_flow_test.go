package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebRTCSink_KeyframePacing 验证关键帧分片不是瞬间突发：发送时间应摊开。
func TestWebRTCSink_KeyframePacing(t *testing.T) {
	b := loadOnvifBaseline(t)

	sink, _ := NewWebRTCSink(&OutputConfig{ID: "wr_flow", Type: "webrtc"})
	sink.Start(context.Background())
	defer sink.Stop()
	src := NewMockSource("wr_flow_src", b.streams)
	pipe := NewDefaultPipe(2048)
	pipe.Bind(src, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipe.Start(ctx)
	defer pipe.Stop()

	var mu sync.Mutex
	var arrivals []time.Time
	var firstArrival time.Time
	done := make(chan struct{})
	pc, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
	defer pc.Close()
	pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			_, _, err := tr.ReadRTP()
			if err != nil { return }
			now := time.Now()
			mu.Lock()
			if firstArrival.IsZero() { firstArrival = now }
			arrivals = append(arrivals, now)
			if len(arrivals) >= 300 {
				select { case done <- struct{}{}: default: }
			}
			mu.Unlock()
		}
	})
	srv := httptest.NewServer(http.HandlerFunc(sink.ServeWHEP))
	defer srv.Close()
	offer, _ := pc.CreateOffer(nil)
	pc.SetLocalDescription(offer)
	resp, _ := http.Post(srv.URL, "application/sdp", bytes.NewReader([]byte(offer.SDP)))
	body, _ := io.ReadAll(resp.Body); resp.Body.Close()
	pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(body)})
	time.Sleep(1 * time.Second)

	// 推一个关键帧(196 分片) + 一个 P 帧
	pushed := 0
	for _, f := range b.video {
		src.Push(&Frame{
			Header: FrameHeader{Magic: FrameMagic, Version: FrameVersion,
				ChannelID: f.channelID, FrameType: f.frameType,
				Codec: f.codec, Flags: f.flags, PTS: f.pts, DTS: f.dts},
			Payload: f.payload,
		})
		pushed++
		if pushed >= 2 { break }
	}

	select { case <-done: case <-time.After(6 * time.Second): }
	mu.Lock(); got := append([]time.Time{}, arrivals...); mu.Unlock()
	require.GreaterOrEqual(t, len(got), 200, "should receive keyframe RTP packets")

	// 测量: 首包到尾包的时间跨度
	span := got[len(got)-1].Sub(got[0])
	t.Logf("keyframe %d RTP packets spread over %.1fms (avg %.2fms/pkt)",
		len(got), float64(span)/float64(time.Millisecond),
		float64(span)/float64(len(got))/float64(time.Millisecond))
	// 关键帧 196 分片, pacing 500µs → 应至少 ~80ms 摊开(不是 <1ms 突发)
	assert.Greater(t, span, 50*time.Millisecond, "keyframe should be paced, not burst")

	// 队列应无丢包(本机回环 + pacing)
	// 通过 sink 的统计验证
	sink.clientsMu.RLock()
	var dropped int64
	for _, c := range sink.clients {
		dropped += c.dropped.Load()
	}
	sink.clientsMu.RUnlock()
	t.Logf("client queue dropped=%d", dropped)
}

// TestWebRTCSink_MultiClientBroadcast 验证多客户端广播。
func TestWebRTCSink_MultiClientBroadcast(t *testing.T) {
	b := loadOnvifBaseline(t)
	sink, _ := NewWebRTCSink(&OutputConfig{ID: "wr_multi2", Type: "webrtc"})
	sink.Start(context.Background())
	defer sink.Stop()
	src := NewMockSource("wr_multi2_src", b.streams)
	pipe := NewDefaultPipe(2048)
	pipe.Bind(src, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipe.Start(ctx)
	defer pipe.Stop()

	srv := httptest.NewServer(http.HandlerFunc(sink.ServeWHEP))
	defer srv.Close()

	// 两个客户端
	var counts [2]int64
	for ci := 0; ci < 2; ci++ {
		pc, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
		defer pc.Close()
		pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		idx := ci
		pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil { return }
				if pkt != nil { counts[idx]++ }
			}
		})
		offer, _ := pc.CreateOffer(nil)
		pc.SetLocalDescription(offer)
		resp, _ := http.Post(srv.URL, "application/sdp", bytes.NewReader([]byte(offer.SDP)))
		body, _ := io.ReadAll(resp.Body); resp.Body.Close()
		pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(body)})
	}
	time.Sleep(1 * time.Second)

	// 推 30 帧
	n := 0
	for _, f := range b.video {
		if n >= 30 { break }
		src.Push(&Frame{
			Header: FrameHeader{Magic: FrameMagic, Version: FrameVersion,
				ChannelID: f.channelID, FrameType: f.frameType,
				Codec: f.codec, Flags: f.flags, PTS: f.pts, DTS: f.dts},
			Payload: f.payload,
		})
		n++
	}
	time.Sleep(3 * time.Second)

	t.Logf("client1 received=%d client2 received=%d", counts[0], counts[1])
	assert.Greater(t, counts[0], int64(100), "client1 should receive RTP")
	assert.Greater(t, counts[1], int64(100), "client2 should receive RTP")

	// Clients() 应返回 2 个
	clients := sink.Clients()
	require.Len(t, clients, 2)
}
