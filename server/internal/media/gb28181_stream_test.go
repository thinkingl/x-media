package media

// gb28181_stream_test.go —— M3 媒体面测试（方案 §5.6/§5.7）：
// INVITE → PS over RTP 推流；PS 结构（pack/PSM/PES/参数集/ADTS）、RTP 规则
// （PT=96、SSRC=y=、MTU、marker）、GOP 重放、BYE 停流、H265 路径、ffmpeg 端到端。

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- 合成码流 ----------

func h264ConfigAnnexB() []byte {
	// SPS + PPS（Annex-B）
	return []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80,
	}
}

func h264AnnexB(key bool, fill byte) []byte {
	var out []byte
	if key {
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2)
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80)
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x65) // IDR
	} else {
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x41) // non-IDR slice
	}
	// 填充若干字节让 PS/PES 有实际负载
	for i := 0; i < 64; i++ {
		out = append(out, fill, byte(i))
	}
	return out
}

func h265ConfigAnnexB() []byte {
	// VPS(32) + SPS(33) + PPS(34)（Annex-B；首字节 = type<<1）
	return []byte{
		0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0x0c, 0x01,
		0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01,
		0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xc0, 0x73,
	}
}

func h265AnnexB(key bool, fill byte) []byte {
	var out []byte
	if key {
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x40, 0x01, 0x0c, 0x01)
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x01, 0x01)
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x44, 0x01, 0xc0, 0x73)
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x26) // IDR_W_RADL(19)
	} else {
		out = append(out, 0x00, 0x00, 0x00, 0x01, 0x02) // TRAIL_R(1)
	}
	for i := 0; i < 64; i++ {
		out = append(out, fill, byte(i))
	}
	return out
}

func videoFrame(pts int64, payload []byte, key bool) *Frame {
	f := testFrame(0, FrameTypeVideo, CodecH264, pts, payload)
	if key {
		f.Header.Flags |= FlagKeyframe
	}
	return f
}

func audioFrame(pts int64, payload []byte) *Frame {
	return testFrame(1, FrameTypeAudio, CodecAAC, pts, payload)
}

// ---------- 断言助手 ----------

// assertRTPBasics RTP 规则（容忍 UDP 丢包/乱序）：
//   - 所有包：PT=96、SSRC=y=、负载 ≤1400；
//   - 序列号连续处时间戳非递减；
//   - marker=true 仅出现在某时间戳组的末包（其后不应再有同时间戳包）——"marker 界定突发"。
func assertRTPBasics(t *testing.T, pkts []rtp.Packet, ssrc uint32) {
	t.Helper()
	require.NotEmpty(t, pkts)

	sorted := append([]rtp.Packet(nil), pkts...)
	sort.Slice(sorted, func(i, j int) bool {
		return int16(sorted[i].SequenceNumber-sorted[j].SequenceNumber) < 0
	})

	for i := range sorted {
		assert.Equal(t, uint8(96), sorted[i].PayloadType, "PT 必须为 96（PS）")
		assert.Equal(t, ssrc, sorted[i].SSRC, "SSRC 必须为 INVITE y= 指定值")
		assert.LessOrEqual(t, len(sorted[i].Payload), 1400, "RTP 负载 ≤ MTU")
	}
	for i := 1; i < len(sorted); i++ {
		if int16(sorted[i].SequenceNumber-sorted[i-1].SequenceNumber) != 1 {
			continue // 序列号跳变（丢包），该点不做时间戳比较
		}
		assert.Falsef(t, int32(sorted[i].Timestamp-sorted[i-1].Timestamp) < 0,
			"序列连续时时间戳应非递减（seq=%d）", sorted[i].SequenceNumber)
	}
	marked := 0
	for i := 0; i < len(sorted); i++ {
		if !sorted[i].Marker {
			continue
		}
		marked++
		if i+1 < len(sorted) {
			assert.NotEqualf(t, sorted[i].Timestamp, sorted[i+1].Timestamp,
				"marker 后不应再有同时间戳包（seq=%d）", sorted[i].SequenceNumber)
		}
	}
	assert.Positive(t, marked, "应捕获到 marker 末包（突发界定）")
}

// psStreamTypes 解析首个 PSM（00 00 01 BC）的 ES 流类型（视频在前）。
func psStreamTypes(ps []byte) []byte {
	idx := bytes.Index(ps, []byte{0x00, 0x00, 0x01, 0xBC})
	if idx < 0 || idx+12 > len(ps) {
		return nil
	}
	// PSM body: [0]=0xE0 [1]=0xFF [2..3]=program_info_length [4..5]=esm_len，esm 从 +6 起
	body := ps[idx+6:]
	if len(body) < 6 {
		return nil
	}
	esmLen := int(body[4])<<8 | int(body[5])
	esm := body[6:]
	if esmLen > len(esm) || esmLen < 4 {
		return nil
	}
	var types []byte
	for off := 0; off+4 <= esmLen; off += 4 {
		types = append(types, esm[off])
	}
	return types
}

// ---------- 测试 ----------

// startGB28181StreamSink 启动注册到 FakePlatform 的 sink（媒体面测试用）；
// extra 可覆盖默认配置（如 TCPRole/RTPMTU/心跳周期）。
func startGB28181StreamSink(t *testing.T, extra ...func(*GB28181Config)) (*GB28181Sink, *fakeSIPPlatform) {
	t.Helper()
	platform := newFakeSIPPlatform(t, gbTestPlatformID, gbTestRealm, gbTestDeviceID, gbTestPassword)
	ip, port := platform.addr()
	cfg := &GB28181Config{
		PlatformIP:        ip,
		PlatformPort:      port,
		PlatformID:        gbTestPlatformID,
		DeviceID:          gbTestDeviceID,
		ChannelID:         gbTestChannelID,
		SIPDomain:         gbTestRealm,
		Password:          gbTestPassword,
		HeartbeatInterval: 60,
		AudioEnable:       true,
	}
	for _, f := range extra {
		if f != nil {
			f(cfg)
		}
	}
	sink, err := NewGB28181Sink(&OutputConfig{
		ID:      "gb28181_stream_test",
		Type:    "gb28181",
		GB28181: cfg,
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	t.Cleanup(func() { _ = sink.Stop() })
	platform.waitEvent(t, "registered", 5*time.Second)
	return sink, platform
}

func gbStreamsH264AAC() []StreamInfo {
	return []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000, CodecConfig: h264ConfigAnnexB()},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 48000, CodecConfig: []byte{0x11, 0x90}},
	}
}

// TestGB28181Sink_InviteStreamsVideoAudio L2：
// INVITE → PS over RTP（视频 NALU/参数集 + 音频 ADTS 并入同一突发）→ BYE 停流。
func TestGB28181Sink_InviteStreamsVideoAudio(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	const ssrc = uint32(0x12345678)
	platform.invite(t, ssrc)

	// 实时写帧（视频 25fps + 音频随行）
	stop := make(chan struct{})
	go func() {
		var videoPTS int64
		var audioPTS int64
		page := byte(0x10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			page++
			key := videoPTS == 0 || page%25 == 0
			_ = sink.WriteFrame(videoFrame(videoPTS, h264AnnexB(key, page), key))
			videoPTS += 3600 // 90kHz 下 25fps
			_ = sink.WriteFrame(audioFrame(audioPTS, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02}))
			audioPTS += 1024 // 48kHz AAC 帧
			time.Sleep(20 * time.Millisecond)
		}
	}()
	defer close(stop)

	pkts := platform.waitRTP(t, 60, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)

	ps := assemblePS(pkts)
	t.Logf("DEBUG ps len=%d BA=%d BC=%d E0=%d 65=%d 67=%d 68=%d C0=%d FFF1=%d",
		len(ps),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0xBA}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0xBC}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0xE0}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0x65}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0x67}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0x68}),
		bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0xC0}),
		bytes.Count(ps, []byte{0xFF, 0xF1}))
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xBA}), "PS pack header")
	assert.GreaterOrEqual(t, bytes.Count(ps, []byte{0x00, 0x00, 0x01, 0xBA}), 2, "每个 AU 一个 pack header")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xBC}), "PSM（关键帧）")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xE0}), "视频 PES")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x65}), "IDR NALU")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x67}), "SPS（关键帧参数集随行）")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x68}), "PPS")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xC0}), "音频 PES（stream_id 0xC0）")
	assert.True(t, bytes.Contains(ps, []byte{0xFF, 0xF1}), "AAC ADTS 同步头")

	types := psStreamTypes(ps)
	require.NotEmpty(t, types, "PSM 应可解析")
	assert.Equal(t, byte(0x1B), types[0], "H264 stream_type=0x1B")
	require.GreaterOrEqual(t, len(types), 2)
	assert.Equal(t, byte(0x0F), types[1], "AAC stream_type=0x0F")

	// BYE → 停止推流
	platform.bye(t)
	before := platform.rtpCount()
	time.Sleep(400 * time.Millisecond)
	after := platform.rtpCount()
	assert.LessOrEqual(t, after-before, 2, "BYE 后不应继续推流（允许在飞包）")
}

// TestGB28181Sink_GOPReplayOnInvite：INVITE 前已缓存的 GOP 应被立即重放（无需等下一关键帧）。
func TestGB28181Sink_GOPReplayOnInvite(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure(gbStreamsH264AAC()))

	// 无订阅者时写入一个 GOP（关键帧 + 2 帧）
	require.NoError(t, sink.WriteFrame(videoFrame(0, h264AnnexB(true, 0x01), true)))
	require.NoError(t, sink.WriteFrame(videoFrame(3600, h264AnnexB(false, 0x02), false)))
	require.NoError(t, sink.WriteFrame(videoFrame(7200, h264AnnexB(false, 0x03), false)))
	require.Equal(t, 3, sink.hub.gopFrames(), "GOP 缓存应记录 3 帧")

	const ssrc = uint32(0x0BADF00D)
	platform.invite(t, ssrc)

	// 不写新帧，依赖重放出流
	pkts := platform.waitRTP(t, 3, 3*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	ps := assemblePS(pkts)
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x65}), "重放首帧应含 IDR")
}

// TestGB28181Sink_InviteStreamsH265：H265 源走同一路径（PSM 0x24、IDR NALU）。
func TestGB28181Sink_InviteStreamsH265(t *testing.T) {
	sink, platform := startGB28181StreamSink(t)
	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH265, CodecName: "H265", ClockRate: 90000, CodecConfig: h265ConfigAnnexB()},
	}))

	const ssrc = uint32(0x11223344)
	platform.invite(t, ssrc)

	stop := make(chan struct{})
	go func() {
		pts := int64(0)
		key := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = sink.WriteFrame(videoFrame(pts, h265AnnexB(key, 0x20), key))
			key = !key
			pts += 3600
			time.Sleep(25 * time.Millisecond)
		}
	}()
	defer close(stop)

	pkts := platform.waitRTP(t, 20, 5*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	ps := assemblePS(pkts)
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0xE0}), "视频 PES")
	assert.True(t, bytes.Contains(ps, []byte{0x00, 0x00, 0x01, 0x26}), "H265 IDR（type 19）")
	types := psStreamTypes(ps)
	require.NotEmpty(t, types)
	assert.Equal(t, byte(0x24), types[0], "H265 stream_type=0x24")

	// KDM 兼容（实测）：H.265 I 帧判定只扫描 4 字节起始码——视频 PES 负载中
	// 每个 NAL 之前都必须是 00 00 00 01（3 字节起始码会使 SPS 识别失败）。
	pesIdx := bytes.Index(ps, []byte{0x00, 0x00, 0x01, 0xE0})
	require.GreaterOrEqual(t, pesIdx, 0, "应有视频 PES")
	payloadStart := pesIdx + 9 + int(ps[pesIdx+8])
	nextAU := bytes.Index(ps[payloadStart:], []byte{0x00, 0x00, 0x01, 0xBA})
	require.Greater(t, nextAU, 0, "应能定位下一 PS 包头")
	payload := ps[payloadStart : payloadStart+nextAU]
	for i := 0; i+2 < len(payload); i++ {
		if payload[i] == 0 && payload[i+1] == 0 && payload[i+2] == 1 && (i == 0 || payload[i-1] != 0) {
			t.Fatalf("视频 PES 中发现 3 字节起始码 @%d（KDM 只识别 00 00 00 01）", i)
		}
	}
	assert.True(t, bytes.Contains(payload, []byte{0x00, 0x00, 0x00, 0x01, 0x42}),
		"SPS 前必须是 4 字节起始码（否则 KDM 不判 I 帧）")
}

// TestGB28181Sink_L3_MP4ToFFmpeg L3：MP4Source → DefaultPipe → Gb28181Sink →
// FakePlatform 收流；重组 PS 后交由 ffmpeg 解复用/解码（视频+音频）。
func TestGB28181Sink_L3_MP4ToFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 未安装，跳过端到端解码校验")
	}
	fixture := testFixturePath(t, "../../test/fixtures/test.mp4")

	sink, platform := startGB28181StreamSink(t)

	src, err := NewMP4Source(&InputConfig{
		ID:   "gb28181_l3_src",
		Type: "file",
		Path: fixture,
		Loop: true,
	})
	require.NoError(t, err)

	pipe := NewDefaultPipe(2048)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()
	defer src.Stop()

	const ssrc = uint32(0x55667788)
	platform.invite(t, ssrc)

	pkts := platform.waitRTP(t, 400, 12*time.Second)
	assertRTPBasics(t, pkts, ssrc)
	ps := assemblePS(pkts)
	require.NotEmpty(t, ps)

	psPath := filepath.Join(t.TempDir(), "out.ps")
	require.NoError(t, os.WriteFile(psPath, ps, 0o644))

	// ffprobe：PS 应含视频+音频两条流
	probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", psPath)
	probeOut, err := probe.CombinedOutput()
	require.NoErrorf(t, err, "ffprobe 应能解析捕获的 PS：%s", string(probeOut))
	out := string(probeOut)
	assert.Contains(t, out, "video", "PS 应含视频流")
	assert.Contains(t, out, "audio", "PS 应含音频流（AAC 直封）")

	// ffmpeg 解码（视频+音频）
	decode := exec.Command("ffmpeg", "-v", "error", "-f", "mpeg", "-i", psPath, "-f", "null", "-")
	decodeOut, err := decode.CombinedOutput()
	require.NoErrorf(t, err, "ffmpeg 应能解码捕获的 PS：%s", string(decodeOut))
}
