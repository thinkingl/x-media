package media

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnalyzeMP4TimestampStats 验证 delta 统计正确识别 onvif 源的时间戳不规则性
// （音频大量停滞/跳变），并识别规则源（test/fixtures/test.mp4）为恒等间隔。
func TestAnalyzeMP4TimestampStats(t *testing.T) {
	onvif := AnalyzeMP4TimestampStats(testFixturePath(t, "../../../test_data/onvif_nvr_12_1_1_25112025075259_25112025075400.mp4"))
	require.NotEmpty(t, onvif, "onvif should be analyzable")

	var audio, video *TimestampDeltaStats
	for i := range onvif {
		if onvif[i].Kind == "audio" {
			audio = &onvif[i]
		}
		if onvif[i].Kind == "video" {
			video = &onvif[i]
		}
	}
	require.NotNil(t, audio, "audio track stats")
	require.NotNil(t, video, "video track stats")

	// 视频为 VFR：不规则，有明显抖动
	require.False(t, video.Regular)
	require.Greater(t, video.JitterMs, float64(5), "onvif video should have jitter")

	// 音频有大量停滞（delta=1 远小于平均 1024 采样）
	require.False(t, audio.Regular)
	require.Greater(t, audio.Stalls, 100, "onvif audio should have many stalls")
	require.Greater(t, audio.Jumps, 50, "onvif audio should have many jumps")
	require.Equal(t, uint32(32000), audio.Timescale)
	require.Equal(t, uint32(90000), video.Timescale)
	require.Equal(t, uint32(90000), video.Timescale)

	// 规则源应识别为恒等间隔（h265_test.mp4 由 ffmpeg 生成，应为 CFR）
	regular := AnalyzeMP4TimestampStats(testFixturePath(t, "../../../test_data/h265_test.mp4"))
	require.NotEmpty(t, regular)
	anyRegular := false
	for i := range regular {
		if regular[i].Regular {
			anyRegular = true
		}
	}
	require.True(t, anyRegular, "h265_test.mp4 should have at least one regular (CFR) track")
}

// TestGridStepForVideo 验证视频网格步进计算（timescale/fps 与中位兜底）。
func TestGridStepForVideo(t *testing.T) {
	onvif := testFixturePath(t, "../../../test_data/onvif_nvr_12_1_1_25112025075259_25112025075400.mp4")
	src, err := NewMP4Source(&InputConfig{ID: "step", Type: "file", Path: onvif, Loop: false})
	require.NoError(t, err)
	require.NoError(t, src.Start(t.Context()))
	defer src.Stop()

	tr := src.baseTr
	require.NotNil(t, tr)
	if tr.isVideo {
		step := gridStepForVideo(tr)
		require.Greater(t, step, int64(3000), "video step should be >3000 (25fps)")
		require.Less(t, step, int64(4500), "video step should be <4500")
	}
}
