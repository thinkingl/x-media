package media

// mp4_source_pace_test.go —— 节流节奏回归测试（CPU 占用问题，2026-10-19）
//
// 背景：test.mp4 / onvif_*.mp4 的音频 stts 严重不规则（70% 样本 delta<=1，
// 即 1/32000 秒）。readLoop 的节流取"所有轨道下一帧最早媒体时间"，
// 音频停滞样本使 nextPaceTarget 计算出的目标时间永远 <= 当前时刻，
// time.Until(target) <= 0 → 不睡眠 → 忙转：实测视频 6278fps / 音频 6428fps
// （应约 25/31fps），CPU 140%。
//
// 断言口径：以真实墙钟为基准，帧数不得超过源标称速率的 1.5 倍。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countFramesByKind 统计窗口内的出帧数与时间跨度。
type frameCounter struct {
	mu    sync.Mutex
	video int
	audio int
	first time.Time
	last  time.Time
}

func (c *frameCounter) onFrame(f *Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.first.IsZero() {
		c.first = now
	}
	c.last = now
	if f.Header.FrameType == FrameTypeVideo {
		c.video++
	} else {
		c.audio++
	}
}

func (c *frameCounter) stats() (video, audio int, span time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.video, c.audio, c.last.Sub(c.first)
}

// TestMP4SourcePacingIrregularAudioStts 音频 stts 不规则（大量 delta=1）时，
// 节流仍必须按墙钟实时节奏出帧，不得忙转。
func TestMP4SourcePacingIrregularAudioStts(t *testing.T) {
	path := testFixturePath(t, "../../test/fixtures/test.mp4")
	src, err := NewMP4Source(&InputConfig{
		ID:   "pace_" + t.Name(),
		Type: "file",
		Path: path,
		Loop: true,
	})
	require.NoError(t, err)

	counter := &frameCounter{}
	src.SetFrameHandler(counter.onFrame)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	defer src.Stop()

	// 采样 3 秒墙钟
	time.Sleep(3 * time.Second)
	video, audio, span := counter.stats()

	require.Greater(t, video, 0, "应有视频帧")
	require.Greater(t, audio, 0, "应有音频帧")
	require.Greater(t, span, time.Second, "采样窗口应覆盖至少 1 秒")

	videoFPS := float64(video) / span.Seconds()
	audioFPS := float64(audio) / span.Seconds()
	t.Logf("实测 video=%.1ffps audio=%.1ffps（窗口 %.2fs，video=%d audio=%d）",
		videoFPS, audioFPS, span.Seconds(), video, audio)

	// test.mp4：视频约 25fps（1443 帧 / 57.87s），音频约 31fps（1801 / 57.73s）。
	// 允许 1.5 倍抖动余量（首帧突发/GOP 重放），超出即判定忙转。
	require.LessOrEqual(t, videoFPS, 25*1.5,
		"视频出帧速率必须接近实时（约 25fps），不得忙转")
	require.LessOrEqual(t, audioFPS, 32*1.5,
		"音频出帧速率必须接近实时（约 31fps），不得忙转")
}

// TestMP4SourcePacingGridAudioIrregularStts 开启 timestamp_grid.audio 后，
// 音频时间戳被网格化（步进 1024），节流也必须按墙钟实时节奏。
func TestMP4SourcePacingGridAudioIrregularStts(t *testing.T) {
	path := testFixturePath(t, "../../test/fixtures/test.mp4")
	src, err := NewMP4Source(&InputConfig{
		ID:   "pacegrid_" + t.Name(),
		Type: "file",
		Path: path,
		Loop: true,
		TimestampGrid: &TrackGridConfig{
			Video: false,
			Audio: true,
		},
	})
	require.NoError(t, err)

	counter := &frameCounter{}
	src.SetFrameHandler(counter.onFrame)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	defer src.Stop()

	time.Sleep(3 * time.Second)
	video, audio, span := counter.stats()

	require.Greater(t, video, 0)
	require.Greater(t, audio, 0)
	require.Greater(t, span, time.Second)

	videoFPS := float64(video) / span.Seconds()
	audioFPS := float64(audio) / span.Seconds()
	t.Logf("grid 模式实测 video=%.1ffps audio=%.1ffps", videoFPS, audioFPS)

	require.LessOrEqual(t, videoFPS, 25*1.5, "网格化音频时视频也不得忙转")
	require.LessOrEqual(t, audioFPS, 32*1.5, "网格化音频后音频速率应接近实时")
}
