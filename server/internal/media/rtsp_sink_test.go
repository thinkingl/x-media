package media

import (
	"context"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
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

// TestRTSPSink_NoGOPDataReplay 验证 writeVideo 不重放历史 GOP 数据帧。
//
// 曾修复的花屏 bug：RTSPSink 周期性重放整个 GOP（含 IDR + 后续 P 帧），
// 历史数据帧与实时流交错，导致 ffmpeg/VLC 解码参考帧错位 → corrupted macroblock。
// 修复后：只周期性重发 SPS/PPS 参数集，不重放数据帧。
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

	// 重置 lastParamSend，确保写首帧时周期参数集触发（与首帧一起发）。
	sink.mu.Lock()
	sink.lastParamSend = time.Time{}
	sink.mu.Unlock()

	// 写一个 P 帧
	pFrame := testFrame(0, FrameTypeVideo, CodecH264, 3000, []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x20})
	require.NoError(t, sink.WriteFrame(pFrame))

	// 首帧 + 参数集：包数应很小（≤5）。
	// 若错误地重放 GOP（几十帧），包数会远大于此。
	sink.mu.RLock()
	stream := sink.stream
	sink.mu.RUnlock()
	require.NotNil(t, stream)

	total := stream.Stats().OutboundRTPPackets
	assert.LessOrEqual(t, total, uint64(5),
		"should only send frame + SPS/PPS, not replay GOP (got %d RTP packets)", total)
}

// TestRTSPSink_PeriodicParamSend 验证周期重发 SPS/PPS（而非数据帧）。
func TestRTSPSink_PeriodicParamSend(t *testing.T) {
	spsPps := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}

	sink := newRTSPTestSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: spsPps},
	}))

	// 已触发过一次参数集，设 lastParamSend 为过去时间，使下次 writeVideo 触发周期参数集。
	sink.mu.Lock()
	sink.lastParamSend = time.Now().Add(-3 * time.Second)
	sink.mu.Unlock()

	// 写一帧，触发周期参数集（SPS/PPS）+ 该帧本身
	pFrame := testFrame(0, FrameTypeVideo, CodecH264, 3000, []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x20})
	require.NoError(t, sink.WriteFrame(pFrame))

	sink.mu.RLock()
	stream := sink.stream
	lastSend := sink.lastParamSend
	sink.mu.RUnlock()
	require.NotNil(t, stream)

	// lastParamSend 应被更新（周期参数集确实触发了）
	assert.False(t, lastSend.IsZero(), "periodic param send should update lastParamSend")

	total := stream.Stats().OutboundRTPPackets
	assert.LessOrEqual(t, total, uint64(5), "should send frame + SPS/PPS only, not GOP")
}
