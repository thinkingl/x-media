package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMP4SourceTimestampGrid 验证网格化重打时间戳：
// 开启后视频/音频帧时间戳按固定步进单调递增，不再跟随源 stts 的
// 停滞/跳变（onvif 源音频 stts 有大量 delta=1 停滞）。
func TestMP4SourceTimestampGrid(t *testing.T) {
	onvif := testFixturePath(t, "../../../test_data/onvif_nvr_12_1_1_25112025075259_25112025075400.mp4")
	src, err := NewMP4Source(&InputConfig{
		ID:   "grid_" + t.Name(),
		Type: "file",
		Path: onvif,
		Loop: false,
		TimestampGrid: &TrackGridConfig{
			Video: true,
			Audio: true,
		},
	})
	require.NoError(t, err)

	sink := NewMockSink("gridsink")
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))

	// 运行 ~2.5s：onvif 视频约 25fps → 应出 ~60 帧视频，音频约 31fps → ~77 帧
	time.Sleep(2500 * time.Millisecond)
	cancel()
	src.Stop()

	frames := sink.Frames()
	require.NotEmpty(t, frames, "should receive frames")

	videoStep := int64(0)
	audioStep := int64(0)
	var prevVideoTS int64 = -1
	var prevAudioTS int64 = -1
	videoCount, audioCount := 0, 0
	for _, f := range frames {
		ts := f.Header.PTS
		switch f.Header.FrameType {
		case FrameTypeVideo:
			if prevVideoTS >= 0 {
				step := ts - prevVideoTS
				if videoStep == 0 {
					videoStep = step
				} else {
					require.Equal(t, videoStep, step, "video grid step must be constant")
				}
				require.Greater(t, ts, prevVideoTS, "video ts must be strictly increasing")
			}
			prevVideoTS = ts
			videoCount++
		case FrameTypeAudio:
			if prevAudioTS >= 0 {
				step := ts - prevAudioTS
				if audioStep == 0 {
					audioStep = step
				} else {
					require.Equal(t, audioStep, step, "audio grid step must be constant")
				}
				require.Greater(t, ts, prevAudioTS, "audio ts must be strictly increasing")
			}
			prevAudioTS = ts
			audioCount++
		}
	}

	t.Logf("video frames=%d step=%d@90000(%.2fms) audio frames=%d step=%d@32000(%.2fms)",
		videoCount, videoStep, float64(videoStep)*1000/90000,
		audioCount, audioStep, float64(audioStep)*1000/32000)

	require.Greater(t, videoCount, 40, "should get enough video frames")
	require.Greater(t, audioCount, 40, "should get enough audio frames")
	// 视频步进应接近 90000/25=3600，音频步进应为 1024（AAC 帧）
	require.Greater(t, videoStep, int64(3000), "video grid step should be near 25fps")
	require.Less(t, videoStep, int64(4500), "video grid step should be near 25fps")
	require.Equal(t, int64(aacSamplesPerFrame), audioStep, "audio grid step should be 1024 samples")
}
