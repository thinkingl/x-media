package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMpeg4GenericAUHeaderByteAligned 防回退：AU-header 区必须是
// 2 字节长度域 + 字节对齐布局 —— ffmpeg（libavformat/rtpdec_mpeg4.c
// rtp_parse_mp4_au：AV_RB16 读长度、buf+=2 后从 bit16 读 size）与
// VLC 等主流客户端均按此解析。此前按 RFC 12bit 紧打包实现导致
// ffmpeg 报 "First AU larger than packet size"、音频零采样。
func TestMpeg4GenericAUHeaderByteAligned(t *testing.T) {
	// AU size=4 → [00 10][4<<3=0x0020]
	assert.Equal(t, []byte{0x00, 0x10, 0x00, 0x20}, mpeg4GenericAUHeader(4))
	// AU size=0x123 (291) → 291<<3 = 0x0918
	assert.Equal(t, []byte{0x00, 0x10, 0x09, 0x18}, mpeg4GenericAUHeader(0x123))
	// AU size=8191（13bit 上限）→ 0xFFF8
	assert.Equal(t, []byte{0x00, 0x10, 0xFF, 0xF8}, mpeg4GenericAUHeader(8191))
	// AU size=357（实测出问题的分叉尺寸段）→ 357<<3 = 0x0B28
	assert.Equal(t, []byte{0x00, 0x10, 0x0B, 0x28}, mpeg4GenericAUHeader(357))
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

// TestParseMPEG4GenericAUsByteAligned 验证输入侧按字节对齐布局解析
// （与 ffmpeg rtp_parse_mp4_au 一致：RB16 长度域，AU-header 从字节2开始）。
func TestParseMPEG4GenericAUsByteAligned(t *testing.T) {
	// 单 AU：size=357 → [00 10][0B 28] + 357 字节负载
	payload := make([]byte, 4+357)
	copy(payload, []byte{0x00, 0x10, 0x0B, 0x28})
	sizes, headerBytes, ok := parseMPEG4GenericAUs(payload, 13, 3)
	require.True(t, ok, "字节对齐布局必须可解析")
	assert.Equal(t, 4, headerBytes)
	require.Len(t, sizes, 1)
	assert.Equal(t, 357, sizes[0], "size≥512 分叉段必须解析正确（此前 12bit 紧打包读出 5712）")

	// 多 AU：2 个 AU（size 100, 200）→ 长度域 = 2*16=32 bits
	p2 := make([]byte, 6+300)
	p2[0], p2[1] = 0x00, 0x20 // AU-headers-length = 32 bits
	h1, h2 := uint16(100)<<3, uint16(200)<<3
	copy(p2[2:], []byte{byte(h1 >> 8), byte(h1), byte(h2 >> 8), byte(h2)})
	sizes2, headerBytes2, ok2 := parseMPEG4GenericAUs(p2, 13, 3)
	require.True(t, ok2)
	assert.Equal(t, 6, headerBytes2)
	assert.Equal(t, []int{100, 200}, sizes2)

	// 畸形：长度域与头尺寸不匹配 → 拒绝
	_, _, ok3 := parseMPEG4GenericAUs([]byte{0x00, 0x10, 0x00}, 13, 3)
	assert.False(t, ok3, "截断包必须拒绝")
}
