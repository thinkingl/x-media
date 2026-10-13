package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMpeg4GenericAUHeaderRFC3640 防回退：AU-header 区必须是 RFC 3640
// 位布局（12bit 长度域 + 13bit size + 3bit index + 补齐）。
func TestMpeg4GenericAUHeaderRFC3640(t *testing.T) {
	// AU size=4 → 01 00 02 00（此前非标准实现为 00 10 00 20）
	assert.Equal(t, []byte{0x01, 0x00, 0x02, 0x00}, mpeg4GenericAUHeader(4))
	// AU size=0x123 (291) → size=0b0_0010_0100_011
	assert.Equal(t, []byte{0x01, 0x00, 0x91, 0x80}, mpeg4GenericAUHeader(0x123))
	// AU size=8191（13bit 上限）
	assert.Equal(t, []byte{0x01, 0x0F, 0xFF, 0x80}, mpeg4GenericAUHeader(8191))
}

// TestRTSPInputReceivesAACAudio 复现 RTSP 输入音频缺失：
// buildStreamsFromDescription 只认 format.Generic（死代码），runSession 也
// 从未注册音频回调 —— 无论对端是否发布 MPEG4-GENERIC AAC，输入都收不到
// 任何音频帧。以本项目 RTSPSink（标准 MPEG4-GENERIC 帧格式）做回环验证。
func TestRTSPInputReceivesAACAudio(t *testing.T) {
	globalRTSPManager.StopAll()
	defer globalRTSPManager.StopAll()

	addr := "127.0.0.1:" + freeTCPPort(t)
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "rtsp_audio_srv_" + t.Name(),
		Type: "rtsp",
		Mode: "server",
		Addr: addr,
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	require.NoError(t, sink.Configure(flvTestStreams()))
	defer sink.Stop()

	src := NewMockSource("rtsp_audio_src_"+t.Name(), flvTestStreams())
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	// 持续喂视频+AAC 交替帧
	stopFeed := make(chan struct{})
	defer close(stopFeed)
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		videoPTS, audioPTS := int64(0), int64(0)
		idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04}
		aac := []byte{0x21, 0x10, 0x04, 0x60} // 原始 AAC AU（1024 采样 @48k）
		for {
			select {
			case <-stopFeed:
				return
			case <-ticker.C:
				videoPTS += 3000
				src.Push(testFrame(0, FrameTypeVideo, CodecH264, videoPTS, idr))
				audioPTS += 1024
				src.Push(testFrame(1, FrameTypeAudio, CodecAAC, audioPTS, aac))
			}
		}
	}()

	input, err := NewRTSPInput(&InputConfig{
		ID:        "rtsp_audio_in_" + t.Name(),
		Type:      "rtsp",
		URL:       "rtsp://" + addr + "/live/" + sink.ID(),
		Transport: "tcp",
	})
	require.NoError(t, err)
	recvSink := NewMockSink("rtsp_audio_recv")
	recvPipe := NewDefaultPipe(2048)
	require.NoError(t, recvPipe.Bind(input, recvSink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, input.Start(ctx))
	require.NoError(t, recvPipe.Start(ctx))
	defer recvPipe.Stop()
	defer input.Stop()

	waitCond(t, 10*time.Second, func() bool {
		return input.Status() == StreamStatusRunning
	}, "RTSP input running")

	// 断言收到音频帧（旧代码一帧都没有）
	waitCond(t, 10*time.Second, func() bool {
		for _, f := range recvSink.Frames() {
			if f.Header.FrameType == FrameTypeAudio {
				return true
			}
		}
		return false
	}, "audio frames received over RTSP")

	var audioFrames, videoFrames []*Frame
	for _, f := range recvSink.Frames() {
		switch f.Header.FrameType {
		case FrameTypeAudio:
			audioFrames = append(audioFrames, f)
		case FrameTypeVideo:
			videoFrames = append(videoFrames, f)
		}
	}
	require.NotEmpty(t, audioFrames, "必须收到音频帧")
	require.NotEmpty(t, videoFrames, "视频帧不受影响")

	// 音频帧字段正确性
	for _, f := range audioFrames {
		assert.Equal(t, uint8(1), f.Header.ChannelID, "音频应为 channel 1（与视频错开）")
		assert.Equal(t, CodecAAC, f.Header.Codec)
		assert.NotEmpty(t, f.Payload, "音频 payload 不能为空")
	}
	// PTS 单调（TCP 回环无丢包）
	for i := 1; i < len(audioFrames); i++ {
		assert.Greater(t, audioFrames[i].Header.PTS, audioFrames[i-1].Header.PTS,
			"音频 PTS 应单调递增")
	}
	// 视频 channel 未错位
	for _, f := range videoFrames {
		assert.Equal(t, uint8(0), f.Header.ChannelID, "视频应保持 channel 0")
	}
}
