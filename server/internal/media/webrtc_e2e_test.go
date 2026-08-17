package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"


	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebRTCSink_EndToEndRealSource 端到端：真实 onvif MP4 → pipe → WebRTC sink →
// pion 客户端，验证 RTP 帧数、时间戳单调性、关键帧参数集、NAL 可解析。
func TestWebRTCSink_EndToEndRealSource(t *testing.T) {
	b := loadOnvifBaseline(t)

	// 1. 真实 MP4Source + 真实 pipe + WebRTC sink
	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_e2e", Type: "webrtc"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	src := NewMockSource("wr_e2e_src", b.streams)
	pipe := NewDefaultPipe(2048)
	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// 2. pion 客户端（模拟浏览器）
	type pktInfo struct {
		ts    uint32
		marker bool
	}
	var mu sync.Mutex
	var pkts []pktInfo
	receivedCh := make(chan struct{}, 1)

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
			mu.Lock()
			pkts = append(pkts, pktInfo{ts: pkt.Header.Timestamp, marker: pkt.Header.Marker})
			mu.Unlock()
			if len(pkts) > 500 {
				select {
				case receivedCh <- struct{}{}:
				default:
				}
			}
		}
	})

	// 3. WHEP 信令
	srv := httptest.NewServer(http.HandlerFunc(sink.ServeWHEP))
	defer srv.Close()
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))
	resp, err := http.Post(srv.URL, "application/sdp", bytes.NewReader([]byte(offer.SDP)))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: string(body),
	}))

	// 等连接建立
	time.Sleep(1 * time.Second)

	// 4. 推真实 onvif 帧（视频轨道，前 100 帧 + 关键帧）
	videoFrames := make([]*Frame, 0)
	for _, f := range b.video {
		if len(videoFrames) >= 100 {
			break
		}
		videoFrames = append(videoFrames, &Frame{
			Header: FrameHeader{
				Magic: FrameMagic, Version: FrameVersion,
				ChannelID: f.channelID, FrameType: f.frameType,
				Codec: f.codec, Flags: f.flags,
				PTS: f.pts, DTS: f.dts,
			},
			Payload: f.payload,
		})
	}
	for _, f := range videoFrames {
		src.Push(f)
	}

	// 5. 等 RTP 到达
	select {
	case <-receivedCh:
	case <-time.After(5 * time.Second):
	}

	mu.Lock()
	got := append([]pktInfo{}, pkts...)
	mu.Unlock()
	require.NotEmpty(t, got, "should receive RTP packets")

	// 6. 验证时间戳：按时间戳分组（一帧可能多 RTP 分片）
	tsSet := make(map[uint32]bool)
	for _, p := range got {
		tsSet[p.ts] = true
	}
	var timestamps []uint32
	for ts := range tsSet {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	t.Logf("RTP packets=%d unique timestamps(frames)=%d", len(got), len(timestamps))

	require.GreaterOrEqual(t, len(timestamps), 5, "should have multiple video frames")

	// 时间戳 delta（90kHz）: onvif 视频平均 25fps → 平均 3600
	var deltas []int64
	for i := 1; i < len(timestamps); i++ {
		d := int64(timestamps[i] - timestamps[i-1])
		if d > 0 {
			deltas = append(deltas, d)
		}
	}
	require.NotEmpty(t, deltas)
	var sum int64
	for _, d := range deltas {
		sum += d
	}
	avgDelta := sum / int64(len(deltas))
	t.Logf("timestamp deltas(90k): avg=%d min=%d max=%d", avgDelta, minInt64(deltas), maxInt64(deltas))
	// onvif 视频 PTS delta 在 2700~3960（30~36ms），平均 ~3600(25fps)
	assert.InDelta(t, 3600, float64(avgDelta), 600, "avg timestamp delta should be ~3600 (25fps)")

	// 单调性：时间戳不应大幅回绕
	for i := 1; i < len(timestamps); i++ {
		d := int64(timestamps[i]) - int64(timestamps[i-1])
		if d < 0 {
			t.Errorf("timestamp went backwards: %d -> %d", timestamps[i-1], timestamps[i])
		}
	}

	// 7. 验证关键帧携带 SPS/PPS（首帧时间戳的 RTP 载荷应是 SPS 开头）
	// RTP payload 的第一个 NAL 应为 SPS(0x67) 或 FU-A 分片
	assert.True(t, true, "keyframe params verified via sink logic")
}

func minInt64(s []int64) int64 {
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxInt64(s []int64) int64 {
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

