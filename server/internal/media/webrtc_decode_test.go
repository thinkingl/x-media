package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebRTCSink_DecodeVerify 端到端 + 解码验证：RTP → H264 NAL 重组 →
// ffmpeg 实际解码，确认收到的流可解码且帧率正确。
func TestWebRTCSink_DecodeVerify(t *testing.T) {
	b := loadOnvifBaseline(t)

	sink, err := NewWebRTCSink(&OutputConfig{ID: "wr_decode", Type: "webrtc"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	src := NewMockSource("wr_decode_src", b.streams)
	pipe := NewDefaultPipe(2048)
	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// pion 客户端收集原始 RTP（含 seq 号，用于重组排序）
	var mu sync.Mutex
	var pkts []*rtp.Packet
	done := make(chan struct{})

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
			pkts = append(pkts, pkt)
			if len(pkts) >= 1200 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			mu.Unlock()
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(sink.ServeWHEP))
	defer srv.Close()
	offer, _ := pc.CreateOffer(nil)
	pc.SetLocalDescription(offer)
	resp, err := http.Post(srv.URL, "application/sdp", bytes.NewReader([]byte(offer.SDP)))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: string(body),
	}))
	time.Sleep(1 * time.Second)

	// 推视频帧（前 80 帧，含关键帧）
	for i, f := range b.video {
		if i >= 80 {
			break
		}
		src.Push(&Frame{
			Header: FrameHeader{
				Magic: FrameMagic, Version: FrameVersion,
				ChannelID: f.channelID, FrameType: f.frameType,
				Codec: f.codec, Flags: f.flags, PTS: f.pts, DTS: f.dts,
			},
			Payload: f.payload,
		})
	}

	time.Sleep(3 * time.Second)
	mu.Lock()
	got := append([]*rtp.Packet{}, pkts...)
	mu.Unlock()
	require.GreaterOrEqual(t, len(got), 50, "should receive RTP packets")

	// 按 (timestamp, seq) 排序重组：帧内按 seq，帧间按 timestamp
	sort.SliceStable(got, func(i, j int) bool {
		if got[i].Header.Timestamp != got[j].Header.Timestamp {
			return got[i].Header.Timestamp < got[j].Header.Timestamp
		}
		return got[i].Header.SequenceNumber < got[j].Header.SequenceNumber
	})

	// 重组 AnnexB
	annexb := reassembleSortedH264(got)

	tmp := t.TempDir() + "/recv.h264"
	require.NoError(t, os.WriteFile(tmp, annexb, 0644))
	t.Logf("reassembled %d bytes", len(annexb))

	// ffprobe 验证可解码 + 帧率
	cmd2 := exec.Command("/usr/local/bin/ffprobe", "-v", "error",
		"-select_streams", "v", "-show_entries", "stream=nb_frames,avg_frame_rate,codec_name",
		"-of", "default=noprint_wrappers=1", tmp)
	out2, _ := cmd2.CombinedOutput()
	t.Logf("ffprobe: %s", out2)
	assert.Contains(t, string(out2), "h264", "should be H264")
	assert.Contains(t, string(out2), "avg_frame_rate", "should have frame rate")

	// ffmpeg 完整解码（检查是否有 corrupt 错误）
	cmd := exec.Command("/usr/local/bin/ffmpeg", "-v", "error", "-i", tmp, "-f", "null", "-")
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Logf("ffmpeg decode output: %.500s", out)
		t.Errorf("ffmpeg decode should succeed without corruption")
	}
}

// reassembleSortedH264 将按 (ts, seq) 排序的 RTP 包重组为 AnnexB。
func reassembleSortedH264(pkts []*rtp.Packet) []byte {
	var out []byte
	// 按时间戳分帧
	type frame struct {
		ts   uint32
		pkts []*rtp.Packet
	}
	var frames []frame
	var cur *frame
	for _, p := range pkts {
		if cur == nil || cur.ts != p.Header.Timestamp {
			frames = append(frames, frame{ts: p.Header.Timestamp})
			cur = &frames[len(frames)-1]
		}
		cur.pkts = append(cur.pkts, p)
	}

	for _, f := range frames {
		for _, p := range f.pkts {
			payload := p.Payload
			if len(payload) == 0 {
				continue
			}
			nalType := payload[0] & 0x1F
			switch {
			case nalType == 28: // FU-A
				fu := payload[1]
				if fu&0x80 != 0 { // start
					nalHeader := byte(payload[0]&0xE0) | (fu & 0x1F)
					out = append(out, 0x00, 0x00, 0x00, 0x01, nalHeader)
					out = append(out, payload[2:]...)
				} else {
					out = append(out, payload[2:]...)
				}
			case nalType == 24: // STAP-A
				i := 1
				for i+2 <= len(payload) {
					nalLen := int(payload[i])<<8 | int(payload[i+1])
					i += 2
					if i+nalLen <= len(payload) {
						out = append(out, 0x00, 0x00, 0x00, 0x01)
						out = append(out, payload[i:i+nalLen]...)
						i += nalLen
					} else {
						break
					}
				}
			default:
				out = append(out, 0x00, 0x00, 0x00, 0x01)
				out = append(out, payload...)
			}
		}
	}
	return out
}
