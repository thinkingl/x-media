package media

// gb28181_bframe_test.go —— B 帧时间戳回归测试（2026-10-20）
//
// 背景：源含 B 帧时（如「远方的你-1080」H.264 High / has_b_frames=2），
// 解码序 PTS 非单调（0,3,1,2,6,4,5…），而 DTS 单调。ISO/IEC 13818-1 与
// GB/T 28181 附录 C 要求 PES 在 PTS≠DTS 时同时携带 PTS+DTS；只写 PTS 会让
// 接收端按 PTS 重排 → WebRTC 预览出现卡顿/回退。
//
// 断言：视频 PES 必须出现 flags=0xC0（PTS+DTS），且 DTS 严格单调。

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pesTimestamp 一个视频 PES 的时间戳（hasDTS=false 表示仅 PTS）。
type pesTimestamp struct {
	pts, dts int64
	hasDTS   bool
}

// pesTimestamps 解析 PS 中所有视频 PES 的 PTS/DTS（按解码序）。
func pesTimestamps(ps []byte) []pesTimestamp {
	var out []pesTimestamp
	for i := 0; i+9 < len(ps); {
		if ps[i] != 0 || ps[i+1] != 0 || ps[i+2] != 1 {
			i++
			continue
		}
		streamID := ps[i+3]
		switch {
		case streamID == 0xBA:
			i += 14 + int(ps[i+13]&0x07)
		case streamID == 0xBB || streamID == 0xBC || (streamID >= 0xE0 && streamID <= 0xEF):
			length := int(binary.BigEndian.Uint16(ps[i+4 : i+6]))
			if streamID == 0xE0 {
				hl := int(ps[i+8])
				h := ps[i+9 : i+9+hl]
				if len(h) >= 5 {
					pts := int64(h[0]>>1&0x07)<<30 | int64(h[1])<<22 | int64(h[2]>>1)<<15 | int64(h[3])<<7 | int64(h[4]>>1)
					ts := pesTimestamp{pts: pts}
					if len(h) >= 10 {
						ts.dts = int64(h[5]>>1&0x07)<<30 | int64(h[6])<<22 | int64(h[7]>>1)<<15 | int64(h[8])<<7 | int64(h[9]>>1)
						ts.hasDTS = true
					}
					out = append(out, ts)
				}
			}
			i += 6 + length
		default:
			i++
		}
	}
	return out
}

// TestGB28181Sink_BFramePESCarriesDTS B 帧流（PTS 非单调、DTS 单调）的
// 视频 PES 必须在 PTS≠DTS 时携带 PTS+DTS，且 DTS 单调递增。
func TestGB28181Sink_BFramePESCarriesDTS(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000,
			CodecConfig: []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1e,
				0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80}},
	}))

	const ssrc = uint32(0x99AABBCC)
	platform.invite(t, ssrc)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		f := int64(3600) // 25fps @ 90kHz
		// 解码序：I0 P3 B1 B2 P6 B4 B5 P9 B7 B8 …
		frames := []struct {
			pts, dts int64
			key      bool
		}{
			{0, 0, true},
			{3 * f, 1 * f, false},
			{1 * f, 2 * f, false},
			{2 * f, 3 * f, false},
			{6 * f, 4 * f, false},
			{4 * f, 5 * f, false},
			{5 * f, 6 * f, false},
			{9 * f, 7 * f, false},
			{7 * f, 8 * f, false},
			{8 * f, 9 * f, false},
		}
		for i := 0; i < 4; i++ {
			base := int64(i) * 10 * f // 跨循环保持 DTS 单调
			for _, fr := range frames {
				select {
				case <-stop:
					return
				default:
				}
				ff := testFrame(0, FrameTypeVideo, CodecH264, base+fr.pts, h264AnnexB(fr.key, byte(0x30+i)))
				ff.Header.DTS = base + fr.dts
				if fr.key {
					ff.Header.Flags |= FlagKeyframe
				}
				_ = sink.WriteFrame(ff)
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	defer func() { close(stop); <-done }()

	pkts := platform.waitRTP(t, 12, 5*time.Second)
	ps := assemblePS(pkts)
	require.NotEmpty(t, ps)

	// RTP 时间戳必须单调（按 DTS 打）：WebRTC/GB 网关常用 RTP 时间戳做
	// 抖动缓冲与排序；B 帧下按 PTS 会出现回退，导致卡顿/回退。
	var rtpTS []uint32
	for _, p := range pkts {
		if len(rtpTS) == 0 || p.Timestamp != rtpTS[len(rtpTS)-1] {
			rtpTS = append(rtpTS, p.Timestamp)
		}
	}
	require.GreaterOrEqual(t, len(rtpTS), 3, "应覆盖多个访问单元")
	for i := 1; i < len(rtpTS); i++ {
		require.GreaterOrEqual(t, rtpTS[i], rtpTS[i-1],
			"RTP 时间戳必须单调不减（DTS），index %d: %d -> %d", i, rtpTS[i-1], rtpTS[i])
	}

	tsList := pesTimestamps(ps)
	require.NotEmpty(t, tsList)

	var dtsList, ptsList []int64
	for _, ts := range tsList {
		ptsList = append(ptsList, ts.pts)
		if ts.hasDTS {
			require.NotEqual(t, ts.pts, ts.dts, "只有 PTS≠DTS 的 PES 才应携带 DTS")
			dtsList = append(dtsList, ts.dts)
		}
	}
	require.NotEmpty(t, dtsList,
		"B 帧流的视频 PES 必须携带 PTS+DTS（flags=0xC0）；当前只写了 PTS")
	require.GreaterOrEqual(t, len(dtsList), 5, "重排帧数量应足够覆盖断言")

	// DTS 在解码序上必须单调递增
	for i := 1; i < len(dtsList); i++ {
		require.Greater(t, dtsList[i], dtsList[i-1],
			"DTS 必须单调递增（解码序），index %d", i)
	}
	// PTS 应保持非单调（证明 B 帧重排确实存在，测试覆盖到位）
	nonMonotonic := false
	for i := 1; i < len(ptsList); i++ {
		if ptsList[i] < ptsList[i-1] {
			nonMonotonic = true
			break
		}
	}
	assert.True(t, nonMonotonic, "测试数据应包含非单调 PTS（B 帧重排）")
}
