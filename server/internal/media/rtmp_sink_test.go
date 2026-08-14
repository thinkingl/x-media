package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRTMPTestSink(t *testing.T, mockAddr string) *RTMPSink {
	t.Helper()
	sink, err := NewRTMPSink(&OutputConfig{
		ID:   "rtmp_" + t.Name(),
		Type: "rtmp",
		URL:  "rtmp://" + mockAddr + "/live/test",
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	return sink
}

func TestRTMPSink_ConnectPublish(t *testing.T) {
	mock := startRTMPMockServer(t)
	defer mock.close()

	sink := newRTMPTestSink(t, mock.addr())
	defer sink.Stop()

	require.NoError(t, sink.Configure(flvTestStreams()))

	// 推一帧触发连接
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	err := sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 3000, idr))
	require.NoError(t, err)

	// mock server 应收到 metadata(18) + video seq header(9) + video data(9)
	waitCond(t, 3*time.Second, func() bool {
		msgs := mock.messages()
		var dataMsgs int
		for _, m := range msgs {
			if m.Type == msgTypeVideo {
				dataMsgs++
			}
		}
		return dataMsgs >= 2 // seq header + data
	}, "RTMP video messages received")

	msgs := mock.messages()
	assert.True(t, hasMetadata(msgs), "should receive onMetaData")
	assert.True(t, hasVideoSeqHeader(msgs), "should receive video sequence header")
}

func TestRTMPSink_AudioVideoData(t *testing.T) {
	mock := startRTMPMockServer(t)
	defer mock.close()

	sink := newRTMPTestSink(t, mock.addr())
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	require.NoError(t, sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 3000, idr)))
	require.NoError(t, sink.WriteFrame(testFrame(1, FrameTypeAudio, CodecAAC, 1024, []byte{0x11, 0x22, 0x33})))

	waitCond(t, 3*time.Second, func() bool {
		msgs := mock.messages()
		var audio, video int
		for _, m := range msgs {
			if m.Type == msgTypeAudio {
				audio++
			}
			if m.Type == msgTypeVideo {
				video++
			}
		}
		return audio >= 2 && video >= 2
	}, "audio+video messages")

	// 验证 video 数据消息时间戳：首帧归一化后为 0（相对流起点）
	for _, m := range mock.messages() {
		if m.Type == msgTypeVideo && len(m.Payload) > 5 && m.Payload[1] == flvAVCPacketTypeNALU {
			assert.Equal(t, uint32(0), m.Ts, "first video data timestamp should be 0 (normalized)")
		}
		if m.Type == msgTypeAudio && len(m.Payload) > 1 && m.Payload[1] == flvAACPacketTypeRaw {
			assert.Equal(t, uint32(0), m.Ts, "first audio data timestamp should be 0 (normalized)")
		}
	}
}

func TestRTMPSink_EndToEndPipe(t *testing.T) {
	mock := startRTMPMockServer(t)
	defer mock.close()

	sink := newRTMPTestSink(t, mock.addr())
	defer sink.Stop()

	src := NewMockSource("rtmp_src", flvTestStreams())
	pipe := NewDefaultPipe(32)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	for i := 0; i < 5; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr))
	}

	waitCond(t, 3*time.Second, func() bool {
		msgs := mock.messages()
		var videoData int
		for _, m := range msgs {
			if m.Type == msgTypeVideo && len(m.Payload) > 5 && m.Payload[1] == flvAVCPacketTypeNALU {
				videoData++
			}
		}
		return videoData >= 5
	}, "5 video data messages")
}

func TestRTMPSink_ConnectionFailure(t *testing.T) {
	sink, err := NewRTMPSink(&OutputConfig{
		ID:   "rtmp_fail",
		Type: "rtmp",
		URL:  "rtmp://127.0.0.1:1/live/test", // 无服务
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()

	err = sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 0, []byte{0, 0, 0, 1, 0x65}))
	assert.Error(t, err, "should fail to connect")
}

func TestRTMPSink_BadURL(t *testing.T) {
	_, err := NewRTMPSink(&OutputConfig{ID: "bad", Type: "rtmp", URL: ""})
	assert.Error(t, err)
}

// ---- 辅助 ----

func hasMetadata(msgs []rtmpMockMsg) bool {
	for _, m := range msgs {
		if m.Type == msgTypeAMF0Data {
			name, _ := parseAMF0String(m.Payload, 0)
			if name == cmdOnMetaData {
				return true
			}
		}
	}
	return false
}

func hasVideoSeqHeader(msgs []rtmpMockMsg) bool {
	for _, m := range msgs {
		if m.Type == msgTypeVideo && len(m.Payload) > 1 && m.Payload[1] == flvAVCPacketTypeSeq {
			return true
		}
	}
	return false
}
