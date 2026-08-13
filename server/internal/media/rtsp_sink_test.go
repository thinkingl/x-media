package media

import (
	"context"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rtspSinkHarness 用 MockSource + DefaultPipe 对接真实 RTSPSink。
type rtspSinkHarness struct {
	sink *RTSPSink
	src  *MockSource
	pipe *DefaultPipe
}

func newRTSPTestSink(t *testing.T) *RTSPSink {
	t.Helper()
	cfg := &OutputConfig{
		ID:   "rtsp_sink_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: "127.0.0.1:0",
	}
	sink, err := NewRTSPSink(cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	return sink
}

func TestRTSPSink_ConfigureCreatesVideoOnlyMedia(t *testing.T) {
	configData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}

	sink := newRTSPTestSink(t)
	defer sink.Stop()

	streams := []StreamInfo{
		{
			ChannelID:   0,
			Kind:        "video",
			CodecID:     CodecH264,
			CodecName:   "H264",
			ClockRate:   90000,
			CodecConfig: configData,
		},
	}
	require.NoError(t, sink.Configure(streams))
	assert.True(t, sink.ready)
	assert.NotNil(t, sink.stream)
	assert.NotNil(t, sink.vMedia)
	assert.Nil(t, sink.aMedia)

	// 路径已注册
	path := "live/" + sink.ID()
	sink.handler.mutex.RLock()
	_, ok := sink.handler.paths[path]
	sink.handler.mutex.RUnlock()
	assert.True(t, ok, "path should be registered")
}

func TestRTSPSink_ConfigureVideoAndAudio(t *testing.T) {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
	aacConfig := []byte{0x11, 0x90} // AAC-LC 48000Hz stereo

	sink := newRTSPTestSink(t)
	defer sink.Stop()

	streams := []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 48000, CodecConfig: aacConfig},
	}
	require.NoError(t, sink.Configure(streams))
	assert.True(t, sink.ready)
	assert.NotNil(t, sink.aMedia, "audio media should be configured")

	// SDP 中的媒体
	desc := sink.stream.Desc
	var videoFound, audioFound bool
	for _, m := range desc.Medias {
		if m.Type == description.MediaTypeVideo {
			videoFound = true
		}
		if m.Type == description.MediaTypeAudio {
			audioFound = true
		}
	}
	assert.True(t, videoFound)
	assert.True(t, audioFound)
}

func TestRTSPSink_ConfigureMissingConfig(t *testing.T) {
	sink := newRTSPTestSink(t)
	defer sink.Stop()

	// 无 CodecConfig 的流 → 应失败
	streams := []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, ClockRate: 90000},
	}
	err := sink.Configure(streams)
	assert.Error(t, err)
}

func TestRTSPSink_WriteVideoRTP(t *testing.T) {
	// 构造合法 SPS/PPS + IDR
	sps := []byte{0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	idr := []byte{0x65, 0x88, 0x80, 0x40, 0x00}

	configData := append([]byte{0, 0, 0, 1}, sps...)
	configData = append(configData, 0, 0, 0, 1)
	configData = append(configData, pps...)

	sink := newRTSPTestSink(t)
	defer sink.Stop()

	streams := []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: configData},
	}
	require.NoError(t, sink.Configure(streams))

	// 构造含 SPS/PPS/IDR 的帧
	frameData := append([]byte{0, 0, 0, 1}, sps...)
	frameData = append(frameData, 0, 0, 0, 1)
	frameData = append(frameData, pps...)
	frameData = append(frameData, 0, 0, 0, 1)
	frameData = append(frameData, idr...)

	err := sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 3000, frameData))
	assert.NoError(t, err)
}

func TestRTSPSink_WriteFrameWhenNotReady(t *testing.T) {
	sink := newRTSPTestSink(t)
	defer sink.Stop()

	// 未 Configure 时 WriteFrame 不应崩溃
	err := sink.WriteFrame(testFrame(0, FrameTypeVideo, CodecH264, 0, []byte{0, 0, 0, 1, 0x65}))
	assert.NoError(t, err)
}

func TestRTSPSink_ClockRateConversion(t *testing.T) {
	// 30fps 视频: 每帧 3000 ticks(90k), RTP 时间戳应保持 3000
	// (因为 ClockRate=90000, To90k 无变化)
	assert.Equal(t, int64(3000), To90k(3000, 90000))
	// 音频 48k: 1s = 48000 ticks → 48000 RTP ticks (ClockRate=sampleRate)
	assert.Equal(t, int64(48000), ConvertClock(48000, 48000, 48000))
}

// TestRTSPSink_EndToEndPipe 验证 MockSource→Pipe→RTSPSink 全链路。
func TestRTSPSink_EndToEndPipe(t *testing.T) {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}

	sink := newRTSPTestSink(t)
	defer sink.Stop()

	src := NewMockSource("rtsp_src", []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	})
	pipe := NewDefaultPipe(32)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// sink 应被 Configure
	waitCond(t, 2*time.Second, func() bool {
		return sink.ready
	}, "sink configured")

	// 推几帧
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00}
	for i := 0; i < 10; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, idr))
	}

	waitCond(t, 2*time.Second, func() bool {
		return pipe.Written() >= 10
	}, "frames written")
}

// TestRTSPSink_NoGOPDataReplay 验证 writeVideo 不重放 GOP 数据帧、也不周期重发参数集。
//
// 曾修复的花屏 bug：RTSPSink 周期性重发 SPS/PPS 参数集（每 2s），
// 参数集作为独立 RTP AU 插入实时流，解码器收到后重新初始化但无 IDR，
// 后续 P 帧参考失效 → 花屏/黑帧直到下个 IDR。
// 修复后：只在 reader PLAY 时发一次参数集（sendParamsToStream），数据面不再插入。
func TestRTSPSink_NoGOPDataReplay(t *testing.T) {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}

	sink := newRTSPTestSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))
	require.True(t, sink.ready)

	// 写多个 P 帧（模拟数据面持续推送），验证不会因周期参数集/GOP 重放产生多余 RTP 包。
	for i := 0; i < 5; i++ {
		pFrame := testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x20})
		require.NoError(t, sink.WriteFrame(pFrame))
	}

	sink.mu.RLock()
	stream := sink.stream
	sink.mu.RUnlock()
	require.NotNil(t, stream)

	// 5 个 P 帧，每个 1 个 RTP 包 = 5（无 reader 时 gortsplib 丢弃，可能 0）。
	// 核心断言：无 GOP 重放/周期参数集产生的额外包（那会是几十到几百）。
	total := stream.Stats().OutboundRTPPackets
	assert.LessOrEqual(t, total, uint64(5),
		"should send only 5 P-frame RTP packets, no extra param/GOP replay (got %d)", total)
}

// TestRTSPSink_NewReaderParams 验证新 reader PLAY 时 sendParamsToStream 只发 SPS/PPS。
func TestRTSPSink_NewReaderParams(t *testing.T) {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}

	sink := newRTSPTestSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))

	// 模拟新 reader PLAY：直接调用 sendParamsToStream
	sink.mu.RLock()
	stream := sink.stream
	sink.mu.RUnlock()
	require.NotNil(t, stream)

	sink.sendParamsToStream(stream)

	// 只发 SPS/PPS 参数集：≤2 个 RTP 包（不重放 GOP）
	total := stream.Stats().OutboundRTPPackets
	assert.LessOrEqual(t, total, uint64(2),
		"new reader should get only SPS/PPS param packets, not GOP (got %d)", total)
}

// TestRTSPSink_ConfigureHevc 验证 RTSPSink 支持 HEVC 视频配置（source 切换场景）。
func TestRTSPSink_ConfigureHevc(t *testing.T) {
	// 用真实 H.265 文件提取 CodecConfig（VPS+SPS+PPS AnnexB）
	src, err := NewMP4Source(&InputConfig{
		ID:   "hevc_cfg_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()
	require.NoError(t, src.Start(ctx))

	streams, err := src.Streams()
	require.NoError(t, err)
	var hevcStream *StreamInfo
	for i := range streams {
		if streams[i].Kind == "video" {
			hevcStream = &streams[i]
			break
		}
	}
	require.NotNil(t, hevcStream)
	require.Equal(t, CodecH265, hevcStream.CodecID, "h265_test.mp4 should be HEVC")
	require.NotEmpty(t, hevcStream.CodecConfig, "should extract HEVC config")

	// 用提取的 HEVC 配置 RTSPSink
	sink := newRTSPTestSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure([]StreamInfo{*hevcStream}))
	assert.True(t, sink.ready, "HEVC configure should succeed")
	assert.True(t, sink.h265, "sink should be in H265 mode")

	// SDP 应为 HEVC 媒体
	sink.mu.RLock()
	desc := sink.stream.Desc
	sink.mu.RUnlock()
	require.NotNil(t, desc)
	var hevcFound bool
	for _, m := range desc.Medias {
		if m.Type == description.MediaTypeVideo {
			for _, f := range m.Formats {
				if _, ok := f.(*format.H265); ok {
					hevcFound = true
				}
			}
		}
	}
	assert.True(t, hevcFound, "SDP should contain H265 format")
}

// TestSplitCodecConfigHevc 验证 HEVC CodecConfig 参数集分离。
func TestSplitCodecConfigHevc(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "hevc_split_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/h265_test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()
	require.NoError(t, src.Start(ctx))

	streams, _ := src.Streams()
	for _, s := range streams {
		if s.Kind == "video" {
			vps, sps, pps := splitCodecConfigHevc(s.CodecConfig)
			assert.NotEmpty(t, vps, "should extract VPS")
			assert.NotEmpty(t, sps, "should extract SPS")
			assert.NotEmpty(t, pps, "should extract PPS")
			// VPS/SPS/PPS 的 HEVC NAL type
			assert.Equal(t, byte(32), vps[0]>>1&0x3F, "VPS type 32")
			assert.Equal(t, byte(33), sps[0]>>1&0x3F, "SPS type 33")
			assert.Equal(t, byte(34), pps[0]>>1&0x3F, "PPS type 34")
			break
		}
	}
}
