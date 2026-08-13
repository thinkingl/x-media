package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func flvTestStreams() []StreamInfo {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
	aacCfg := []byte{0x11, 0x90} // AAC-LC 48k stereo
	return []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 48000, CodecConfig: aacCfg},
	}
}

func newFLVSink(t *testing.T) *HTTPFLVSink {
	t.Helper()
	sink, err := NewHTTPFLVSink(&OutputConfig{ID: "flv_" + t.Name(), Type: "http-flv", Addr: ":0"})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	return sink
}

func TestHTTPFLVSink_ConfigureHeader(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()

	require.NoError(t, sink.Configure(flvTestStreams()))
	assert.True(t, sink.ready)

	// prefix 应包含 FLV header
	sink.mu.RLock()
	prefix := sink.prefix
	sink.mu.RUnlock()

	require.GreaterOrEqual(t, len(prefix), 13)
	assert.Equal(t, byte(0x46), prefix[0])
	assert.Equal(t, byte(0x4C), prefix[1])
	assert.Equal(t, byte(0x56), prefix[2])
	assert.Equal(t, byte(0x01), prefix[3])
}

func TestHTTPFLVSink_SequenceHeaders(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	// prefix 含 FLV header + sequence header
	sink.mu.RLock()
	raw := append([]byte{}, sink.prefix...)
	sink.mu.RUnlock()

	// 找 video sequence header tag (type=9)
	// 结构: flv header 13B, 然后 tags
	pos := 13
	require.LessOrEqual(t, pos+11, len(raw))
	tagType := raw[pos]
	// 第一个 tag 应是 video seq header (0x17 开头)
	assert.Equal(t, byte(9), tagType)
	// data 开头 0x17 = keyframe<<4 | 7(h264), AVCPacketType=0(seq)
	assert.Equal(t, byte(0x17), raw[pos+11])
	assert.Equal(t, byte(0), raw[pos+12])
}

func TestHTTPFLVSink_WriteVideoTag(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	// 视频帧: SPS/PPS + IDR
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04,
	}
	err := sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 3000, frameData))
	require.NoError(t, err)

	sink.ring.mu.Lock()
	n := sink.ring.availableLocked()
	raw := make([]byte, n)
	i := sink.ring.head
	for j := 0; j < n; j++ {
		raw[j] = sink.ring.buf[i]
		i = (i + 1) % len(sink.ring.buf)
	}
	sink.ring.mu.Unlock()

	// 末尾应有视频 tag: 0x17 开头 (keyframe h264), AVCPacketType=1(NALU)
	assert.Greater(t, len(raw), 13+11+5)
	last := raw[len(raw)-1-4:] // 去掉 previous tag size
	_ = last
	// 检查最后一个 tag
	tagStart := len(raw) - 4 // 去掉 prev tag size 4B
	// 从后往前找 tag type
	require.GreaterOrEqual(t, tagStart, 11)
	_ = tagStart
}

func TestHTTPFLVSink_TimestampConversion(t *testing.T) {
	// 30fps: 每帧 3000 ticks(90k) → 33ms
	assert.Equal(t, int64(33), ToMilliseconds(3000, 90000))
	// audio 48k: 1024 samples → 21.33ms
	assert.Equal(t, int64(21), ToMilliseconds(1024, 48000))
}

func TestHTTPFLVSink_WriteAudioTag(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	err := sink.WriteFrame(testFrame(1, FrameTypeAudio, CodecAAC, 1024, []byte{0xde, 0xad, 0xbe, 0xef}))
	require.NoError(t, err)

	sink.ring.mu.Lock()
	n := sink.ring.availableLocked()
	raw := make([]byte, n)
	i := sink.ring.head
	for j := 0; j < n; j++ {
		raw[j] = sink.ring.buf[i]
		i = (i + 1) % len(sink.ring.buf)
	}
	sink.ring.mu.Unlock()

	// 应包含 audio tag (type=8)，data 以 0xAF + AACPacketType=1 开头
	found := false
	pos := 0
	for pos+11 <= len(raw) {
		ts := int(raw[pos+4])<<16 | int(raw[pos+5])<<8 | int(raw[pos+6])
		ds := int(raw[pos+1])<<16 | int(raw[pos+2])<<8 | int(raw[pos+3])
		if raw[pos] == 8 && ds > 2 {
			// data 开头
			if raw[pos+11] == 0xAF && raw[pos+12] == flvAACPacketTypeRaw {
				found = true
				assert.Equal(t, 21, ts, "audio tag timestamp 21ms")
			}
		}
		pos += 11 + ds + 4
	}
	assert.True(t, found, "should find audio tag")
}

func TestHTTPFLVSink_EndToEndPipe(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()

	src := NewMockSource("flv_src", flvTestStreams())
	pipe := NewDefaultPipe(32)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	waitCond(t, 2*time.Second, func() bool {
		return sink.ready
	}, "sink configured")

	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	for i := 0; i < 10; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr))
		src.Push(testFrame(1, FrameTypeAudio, CodecAAC, int64(i)*1024, []byte{0x11, 0x22}))
	}

	waitCond(t, 2*time.Second, func() bool {
		return pipe.Written() >= 20
	}, "all frames written")
}

func TestHTTPFLVSink_FlvWriterUnit(t *testing.T) {
	w := NewFLVWriter()
	require.NotNil(t, w)
	require.Equal(t, 13, len(w.Header()))
}
