package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mp4SourceHarness 用 MockPipe + MockSink 对接真实 MP4Source。
type mp4SourceHarness struct {
	source *MP4Source
	sink   *MockSink
	pipe   *MockPipe
	cancel context.CancelFunc
}

func newMP4SourceHarness(t *testing.T, path string, loop bool) *mp4SourceHarness {
	t.Helper()
	src, err := NewMP4Source(&InputConfig{
		ID:   "mp4_test_" + t.Name(),
		Type: "file",
		Path: path,
		Loop: loop,
	})
	require.NoError(t, err)

	sink := NewMockSink("sink_" + t.Name())
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	return &mp4SourceHarness{source: src, sink: sink, pipe: pipe, cancel: cancel}
}

func (h *mp4SourceHarness) close() {
	h.cancel()
	h.source.Stop()
}

func TestMP4Source_SubscribeStreams(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	resp, err := h.source.Subscribe(context.Background(), &SubscribeRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Streams, "should expose streams")

	var video, audio *StreamInfo
	for i := range resp.Streams {
		s := &resp.Streams[i]
		if s.Kind == "video" {
			video = s
		} else if s.Kind == "audio" {
			audio = s
		}
	}
	require.NotNil(t, video, "should have video stream")
	require.NotNil(t, audio, "should have audio stream")

	assert.Equal(t, CodecH264, video.CodecID)
	assert.Equal(t, 1920, video.Parameters["width"])
	assert.Equal(t, 1080, video.Parameters["height"])
	assert.Equal(t, 90000, video.ClockRate)
	assert.NotEmpty(t, video.CodecConfig, "video should carry SPS/PPS")

	assert.Equal(t, CodecAAC, audio.CodecID)
	assert.Equal(t, 32000, audio.Parameters["sample_rate"])
	assert.Equal(t, 32000, audio.ClockRate)
	assert.Len(t, audio.CodecConfig, 2, "AAC config should be 2 bytes")
}

func TestMP4Source_EmitsKeyframeFirst(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return h.sink.FrameCount() > 0
	}, "first frame")

	frames := h.sink.Frames()
	require.NotEmpty(t, frames)
	// 首个视频帧应有关键帧标志
	foundKey := false
	for _, f := range frames {
		if f.Header.FrameType == FrameTypeVideo {
			assert.Equal(t, FlagKeyframe, f.Header.Flags&FlagKeyframe, "first video frame should be keyframe")
			foundKey = true
			break
		}
	}
	assert.True(t, foundKey, "should have video frames")
}

func TestMP4Source_VideoPTSMonotonic(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) >= 60
	}, "60 video frames")

	// 视频 PTS 单调递增
	lastPTS := int64(-1)
	for _, f := range h.sink.Frames() {
		if f.Header.FrameType != FrameTypeVideo {
			continue
		}
		assert.Greater(t, f.Header.PTS, lastPTS, "video PTS should be monotonic")
		lastPTS = f.Header.PTS
	}
}

func TestMP4Source_AnnexBSamples(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > 0
	}, "first video frame")

	for _, f := range h.sink.Frames() {
		if f.Header.FrameType != FrameTypeVideo {
			continue
		}
		// AnnexB: 每个 sample 以 start code 开头
		assert.GreaterOrEqual(t, len(f.Payload), 4)
		assert.Equal(t, byte(0x00), f.Payload[0])
		assert.Equal(t, byte(0x00), f.Payload[1])
		// NAL type 合法（0-31）
		nalType := f.Payload[4] & 0x1F
		assert.LessOrEqual(t, nalType, byte(31), "valid H.264 NAL type")
	}
}

func TestMP4Source_AudioFramesEmitted(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return audioFrameCount(h.sink) > 0
	}, "audio frames")

	for _, f := range h.sink.Frames() {
		if f.Header.FrameType != FrameTypeAudio {
			continue
		}
		assert.Equal(t, CodecAAC, f.Header.Codec)
		assert.NotEmpty(t, f.Payload, "audio frame should have payload")
	}
}

func TestMP4Source_AVInterleaved(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > 0 && audioFrameCount(h.sink) > 0
	}, "both video and audio")

	transitions := 0
	last := FrameType(255)
	for _, f := range h.sink.Frames() {
		if last != 255 && f.Header.FrameType != last {
			transitions++
		}
		last = f.Header.FrameType
	}
	assert.Greater(t, transitions, 0, "video/audio should interleave")
}

func TestMP4Source_GetStreamInfoSignal(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	sig := mustSignal(t, SignalGetStreamInfo, 1, nil)
	reply, err := h.pipe.SendSignal(context.Background(), sig)
	require.NoError(t, err)
	require.True(t, reply.IsReply)
	require.Equal(t, SignalGetStreamInfo, reply.Type)

	var resp SubscribeResponse
	require.NoError(t, reply.DecodePayload(&resp))
	assert.Equal(t, len(resp.Streams), 2)
}

func TestMP4Source_PauseResume(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	// 先收一批帧
	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) >= 10
	}, "10 video frames")

	before := videoFrameCount(h.sink)

	// Pause
	_, err := h.source.Signal(context.Background(), mustSignal(t, SignalPause, 2, nil))
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	after := videoFrameCount(h.sink)
	assert.Equal(t, before, after, "no frames while paused")

	// Resume
	_, err = h.source.Signal(context.Background(), mustSignal(t, SignalResume, 3, nil))
	require.NoError(t, err)

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > after
	}, "frames after resume")
}

func TestMP4Source_NonexistentFile(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{ID: "bad", Path: "/nonexistent.mp4"})
	require.NoError(t, err)
	err = src.Start(context.Background())
	assert.Error(t, err)
	assert.Equal(t, StreamStatusError, src.Status())
}

func TestMP4Source_LoopWraps(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), true)
	defer h.close()

	// 等超过一个文件时长，验证循环持续产帧
	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > 0
	}, "frames start")

	first := videoFrameCount(h.sink)
	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > first+10
	}, "loop continues")
}

// TestMP4Source_LoopAlignDuration 验证统一基准回绕的换算逻辑。
func TestMP4Source_LoopAlignDuration(t *testing.T) {
	// 基准 90k，累计一圈 270000 ticks (3s)，换算到 32k 应为 96000 (3s)
	baseStart := int64(270000)
	assert.Equal(t, int64(96000), ConvertClock(baseStart, 90000, 32000))

	// 再累计一圈 (6s)
	baseStart2 := int64(540000)
	assert.Equal(t, int64(192000), ConvertClock(baseStart2, 90000, 32000))

	// 反向：32k → 90k
	assert.Equal(t, int64(270000), ConvertClock(96000, 32000, 90000))
}

// TestMP4Source_SelectBaseTrack 验证基准 track 选择（绝对时长最长者）。
func TestMP4Source_SelectBaseTrack(t *testing.T) {
	// 用真实文件构造 tracks，验证视频（更长）被选为基准
	src, err := NewMP4Source(&InputConfig{
		ID:   "base_sel_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)

	// 不启动，直接构造 tracks（Start 会启动 readLoop，测试中不便）
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()
	require.NoError(t, src.Start(ctx))

	// 等流起来（验证 Start 正常）
	waitCond(t, 3*time.Second, func() bool {
		src.mu.RLock()
		defer src.mu.RUnlock()
		return src.tracks != nil && src.baseTr != nil
	}, "tracks built")

	src.mu.RLock()
	base := src.baseTr
	require.NotNil(t, base)
	assert.True(t, base.isBase)
	assert.Equal(t, "video", base.info.Kind, "video (longer) should be base track")
	// 验证基准时长 >= 音频时长
	for _, tr := range src.tracks {
		if tr != base {
			assert.GreaterOrEqual(t, base.trackAbsDuration(), tr.trackAbsDuration(),
				"base track should be the longest")
		}
	}
	src.mu.RUnlock()
}

// TestMP4Source_AlignLoopAlignment 手动触发回绕，验证音视频绝对时间轴一致。
func TestMP4Source_AlignLoopAlignment(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "align_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()
	require.NoError(t, src.Start(ctx))

	waitCond(t, 3*time.Second, func() bool {
		src.mu.RLock()
		defer src.mu.RUnlock()
		return src.baseTr != nil
	}, "tracks built")

	// 触发两次回绕，验证：
	// 1. 基准 track loopDur 累加自身时长
	// 2. 其他 track loopDur = 基准绝对时间换算，绝对时间一致
	src.alignLoopDuration() // 第 1 圈（内部加锁）
	src.mu.RLock()
	firstLoop := src.baseTr.loopDur
	src.mu.RUnlock()
	src.alignLoopDuration() // 第 2 圈
	src.mu.RLock()
	secondLoop := src.baseTr.loopDur
	src.mu.RUnlock()

	assert.Equal(t, firstLoop*2, secondLoop, "base loopDur doubles each loop")

	// 绝对时间一致性：视频与音频的 loopDur 换算后绝对时间应相等
	src.mu.RLock()
	var videoLoop, audioLoop int64
	for _, tr := range src.tracks {
		if tr.info.Kind == "video" {
			videoLoop = ConvertClock(tr.loopDur, int(tr.timescale), 1000000)
		} else if tr.info.Kind == "audio" {
			audioLoop = ConvertClock(tr.loopDur, int(tr.timescale), 1000000)
		}
	}
	src.mu.RUnlock()

	// 换算到统一 1MHz 量纲后，视频与音频的绝对起点应一致
	assert.InDelta(t, float64(videoLoop), float64(audioLoop), 1.0,
		"video and audio loop start should align on absolute timeline")
}

func TestMP4Source_Seek(t *testing.T) {
	h := newMP4SourceHarness(t, testFixturePath(t, "../../test/fixtures/test.mp4"), false)
	defer h.close()

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) > 0
	}, "initial frames")

	// Seek 到中部
	targetPTS := int64(90000 * 10) // 10s at 90k
	_, err := h.source.Signal(context.Background(), mustSignal(t, SignalSeek, 5, &SeekRequest{ChannelID: 0, PTS: targetPTS}))
	require.NoError(t, err)

	waitCond(t, 3*time.Second, func() bool {
		return videoFrameCount(h.sink) >= 5
	}, "frames after seek")

	// 找 seek 后的第一帧，PTS 应接近目标
	var firstAfterSeek int64 = -1
	count := 0
	for _, f := range h.sink.Frames() {
		if f.Header.FrameType == FrameTypeVideo && f.Header.PTS >= targetPTS-90000 {
			if firstAfterSeek == -1 {
				firstAfterSeek = f.Header.PTS
			}
			count++
		}
	}
	assert.Greater(t, count, 0, "should have frames after seek point")
	assert.Less(t, firstAfterSeek-targetPTS, int64(90000*2), "should resume near seek point")
}

// ---- 辅助 ----

func videoFrameCount(s *MockSink) int {
	n := 0
	for _, f := range s.Frames() {
		if f.Header.FrameType == FrameTypeVideo {
			n++
		}
	}
	return n
}

func audioFrameCount(s *MockSink) int {
	n := 0
	for _, f := range s.Frames() {
		if f.Header.FrameType == FrameTypeAudio {
			n++
		}
	}
	return n
}

// ---- 回归测试：loop 回绕 ----
//
// 曾修复的 bug：readLoop 判断 track 读完用 `if err != nil { if done }`，
// 但 nextSample 读完时返回 (nil, true, nil)，err=nil，导致 done 信号丢失、
// baseDone 永不置位、loop 回绕不触发。文件读完后流卡死（不产帧），
// 新 RTSP reader 连上的是停滞流 → ffmpeg h264 none / VLC 黑屏。
//
// 以下测试用加速节流驱动 readLoop 跑完整个文件并回绕，验证：
//  1. 回绕发生：总帧数超过单文件帧数
//  2. 时间戳单调：视频 PTS 跨回绕点持续递增（loopDur 累加生效）

// TestMP4Source_LoopWrapsBeyondFile 验证 loop=true 时文件读完能回绕继续产帧。
func TestMP4Source_LoopWrapsBeyondFile(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "loop_beyond_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)

	sink := NewMockSink("loop_beyond_sink")
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()

	// 加速节流（1ms），使 readLoop 快速读完文件并触发回绕
	src.mu.Lock()
	src.throttleOverride = time.Millisecond
	src.mu.Unlock()

	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))

	// 单文件帧数：视频 1443 + 音频 1801 = 3244
	// 等 readLoop 跑完整个文件并回绕（至少 4000 帧）
	waitCond(t, 30*time.Second, func() bool {
		return sink.FrameCount() >= 4000
	}, "frames beyond single file")

	videoFrames := videoFrameCount(sink)
	audioFrames := audioFrameCount(sink)

	// 视频必然超过单文件视频帧数（1443），证明回绕发生
	assert.Greater(t, videoFrames, 1443, "video should loop beyond single file")
	// 音频也超过单文件帧数
	assert.Greater(t, audioFrames, 1801, "audio should loop beyond single file")
}

// TestMP4Source_LoopPTSMonotonic 验证跨回绕点视频 PTS 单调递增。
func TestMP4Source_LoopPTSMonotonic(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "loop_pts_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: true,
	})
	require.NoError(t, err)

	sink := NewMockSink("loop_pts_sink")
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()

	src.mu.Lock()
	src.throttleOverride = time.Millisecond
	src.mu.Unlock()

	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))

	// 等到回绕后（超过单文件视频帧数）
	waitCond(t, 30*time.Second, func() bool {
		return videoFrameCount(sink) > 1443+50
	}, "video loops into second round")

	frames := sink.Frames()
	var videoPTS []int64
	for _, f := range frames {
		if f.Header.FrameType == FrameTypeVideo {
			videoPTS = append(videoPTS, f.Header.PTS)
		}
	}

	require.Greater(t, len(videoPTS), 1443+50, "should have video frames across loop boundary")

	// 跨回绕点 PTS 必须严格单调递增（不允许回退/重复）
	for i := 1; i < len(videoPTS); i++ {
		assert.Greater(t, videoPTS[i], videoPTS[i-1],
			"video PTS must be strictly increasing at index %d (loop align bug)", i)
	}
}

// TestMP4Source_LoopSingleFileDoesNotLoop 验证 loop=false 时读完即停（无回绕）。
func TestMP4Source_LoopSingleFileDoesNotLoop(t *testing.T) {
	src, err := NewMP4Source(&InputConfig{
		ID:   "single_" + t.Name(),
		Type: "file",
		Path: testFixturePath(t, "../../test/fixtures/test.mp4"),
		Loop: false,
	})
	require.NoError(t, err)

	sink := NewMockSink("single_sink")
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()

	src.mu.Lock()
	src.throttleOverride = time.Millisecond
	src.mu.Unlock()

	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))

	// 等单文件跑完（视频 1443，到 1444 帧即可确认读完）
	waitCond(t, 30*time.Second, func() bool {
		return videoFrameCount(sink) >= 1443
	}, "single file video frames complete")

	// 给足时间确认不额外产帧
	time.Sleep(500 * time.Millisecond)
	after := videoFrameCount(sink)
	assert.Equal(t, 1443, after, "single file (loop=false) should stop at 1443 video frames")
}
