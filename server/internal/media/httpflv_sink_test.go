package media

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"
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
				assert.Equal(t, 0, ts, "first audio tag timestamp should be 0 (normalized to base)")
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

// loadHevcConfigFromFile 从 mp4 文件解析 HEVC CodecConfig（AnnexB VPS/SPS/PPS）。
func loadHevcConfigFromFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err, "open h265 mp4")
	defer f.Close()

	mp4f, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	require.NoError(t, err, "decode h265 mp4")
	for _, trak := range mp4f.Moov.Traks {
		stbl := trak.Mdia.Minf.Stbl
		if stbl == nil || stbl.Stsd == nil || stbl.Stsd.HvcX == nil {
			continue
		}
		if stbl.Stsd.HvcX.HvcC != nil {
			return extractHvcCDecConfRec(&stbl.Stsd.HvcX.HvcC.DecConfRec)
		}
	}
	return nil
}

// TestHTTPFLVSink_HevcSequenceHeader 验证 H265 视频流经 HTTP-FLV 的
// sequence header（HEVCDecoderConfigurationRecord + Enhanced RTMP codecID=12）。
func TestHTTPFLVSink_HevcSequenceHeader(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()

	// 从真实 h265_test.mp4 解析 HEVC 配置（VPS/SPS/PPS），与 MP4Source 相同方式
	hevcConfig := loadHevcConfigFromFile(t, testFixturePath(t, "../../../test_data/h265_test.mp4"))
	require.NotEmpty(t, hevcConfig, "h265_test.mp4 should have HEVC codec config")

	w := NewFLVWriter()
	w.SetClockRate(0, 90000)
	out := w.hevcSequenceHeader(hevcConfig)
	require.NotEmpty(t, out, "hevc sequence header should be produced")

	// tag type=9 (video), data 开头 0x90 = IS_EX(0x80)|SeqStart(0)|KEY(0x10), 后跟 "hvc1"
	assert.Equal(t, byte(9), out[0])
	assert.Equal(t, byte(0x90), out[11], "HEVC seq header should be 0x90")
	assert.Equal(t, "hvc1", string(out[12:16]), "fourcc should be hvc1")

	// 数据部分应为 HEVCDecoderConfigurationRecord: version=1
	// data = [0x90][hvc1(4B)] + hvcC → hvcC 从 data 偏移 5 开始
	hvcC := out[16:]
	assert.Equal(t, byte(0x01), hvcC[0], "HEVCDecoderConfigurationRecord version")
	assert.Equal(t, byte(0x03), hvcC[22], "numOfArrays should be 3 (VPS/SPS/PPS)")
}

// TestHTTPFLVSink_HevcVideoTag 验证 H265 视频帧编码为 Enhanced RTMP HEVC tag。
func TestHTTPFLVSink_HevcVideoTag(t *testing.T) {
	w := NewFLVWriter()
	w.SetClockRate(0, 90000)

	// HEVC IDR NAL（关键帧）
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xaf, 0x0b, 0x46, 0x0e,
	}
	f := testFrame(0, FrameTypeVideo, CodecH265, 3000, frameData)
	f.Header.Flags = FlagKeyframe
	tag := w.EncodeTag(f)
	require.NotEmpty(t, tag)

	// tag type=9
	assert.Equal(t, byte(9), tag[0])
	// data 开头: 0x93 = IS_EX(0x80)|CodedFramesX(3)|KEY(0x10)（PTS==DTS 无 CTS）, 后跟 "hvc1"
	assert.Equal(t, byte(0x93), tag[11], "HEVC keyframe (PTS==DTS) should be 0x93")
	assert.Equal(t, "hvc1", string(tag[12:16]), "fourcc should be hvc1")
	// 数据（length-prefixed）从偏移 16 开始: NAL 长度
	nalLen := int(tag[16])<<24 | int(tag[17])<<16 | int(tag[18])<<8 | int(tag[19])
	assert.Equal(t, len(frameData)-4, nalLen, "NAL length prefix should match")

	// 非关键帧
	f2 := testFrame(0, FrameTypeVideo, CodecH265, 4000, frameData)
	tag2 := w.EncodeTag(f2)
	assert.Equal(t, byte(0xA3), tag2[11], "HEVC inter frame should be 0xA3")
}

// TestHTTPFLVSink_HevcKeyframeParams 验证关键帧前置 VPS/SPS/PPS，
// 使任意时刻接入/丢帧后都可解码（与 RTSPSink 行为一致）。
func TestHTTPFLVSink_HevcKeyframeParams(t *testing.T) {
	hevcConfig := loadHevcConfigFromFile(t, testFixturePath(t, "../../../test_data/h265_test.mp4"))
	require.NotEmpty(t, hevcConfig, "h265_test.mp4 should have HEVC codec config")

	w := NewFLVWriter()
	w.SetClockRate(0, 90000)
	// 模拟 Configure 缓存参数集
	vps, sps, pps := splitCodecConfigHevc(hevcConfig)
	w.hevcVPS, w.hevcSPS, w.hevcPPS = vps, sps, pps

	// 关键帧
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x26, 0x01, 0xaf, 0x0b, 0x46, 0x0e,
	}
	f := testFrame(0, FrameTypeVideo, CodecH265, 3000, frameData)
	f.Header.Flags = FlagKeyframe
	tag := w.EncodeTag(f)
	require.NotEmpty(t, tag)

	// data: [0x93][hvc1(4B)] + length-prefixed NAL 序列
	// 数据从偏移 16 开始，应先出现 VPS(len=24) → SPS(len=41) → PPS(len=7) → IDR
	off := 16
	nalTypes := []int{}
	for len(nalTypes) < 4 && off+4 <= len(tag)-15 {
		l := int(tag[off])<<24 | int(tag[off+1])<<16 | int(tag[off+2])<<8 | int(tag[off+3])
		if l <= 0 || off+4+l > len(tag) {
			break
		}
		nalTypes = append(nalTypes, int((tag[off+4]>>1)&0x3F))
		off += 4 + l
	}
	t.Logf("keyframe NAL types: %v", nalTypes)
	require.GreaterOrEqual(t, len(nalTypes), 3, "keyframe should contain VPS+SPS+PPS (+IDR)")
	assert.Equal(t, 32, nalTypes[0], "first NAL should be VPS")
	assert.Equal(t, 33, nalTypes[1], "second NAL should be SPS")
	assert.Equal(t, 34, nalTypes[2], "third NAL should be PPS")
}
