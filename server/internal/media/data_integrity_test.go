package media

// 数据完整性测试：source / pipe / sink 三个组件分别验证，
// 全部使用 mp4 抽取的共享基准数据（baseline_test.go），每个测试覆盖 3 轮 loop，
// 逐字节对比，要求不丢一帧、不错一位。

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onvifBaselinePath 基准数据源：onvif 测试 mp4。
// 相对本测试文件（server/internal/media）向上 3 级即仓库根目录。
func onvifBaselinePath() string {
	return absRepoPath("../../../test_data/onvif_nvr_12_1_1_25112025075259_25112025075400.mp4")
}

// absRepoPath 将相对本测试文件目录的路径转为绝对路径。
func absRepoPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), rel)
}

// loadOnvifBaseline 载入基准数据并打印规模。
func loadOnvifBaseline(t *testing.T) *baseline {
	t.Helper()
	b := loadBaseline(t, onvifBaselinePath())
	t.Logf("baseline: video=%d audio=%d streams=%d totalDur=%d",
		len(b.video), len(b.audio), len(b.streams), b.totalDur)
	return b
}

// compareBaseline 逐字节比对 got 与 want（每帧 payload + pts/dts/flags/codec/channel）。
// got 允许比 want 多（来源继续运行），只取前 len(want) 帧参与比对。
func compareBaseline(t *testing.T, label string, got, want []baselineFrame) {
	t.Helper()
	require.GreaterOrEqual(t, len(got), len(want),
		"%s: got %d frames < want %d (丢帧)", label, len(got), len(want))
	got = got[:len(want)]

	for i := range want {
		w, g := want[i], got[i]
		if !assert.Equal(t, w.pts, g.pts, "%s frame %d pts", label, i) {
			t.Fatalf("pts mismatch at %s frame %d", label, i)
		}
		assert.Equal(t, w.dts, g.dts, "%s frame %d dts", label, i)
		assert.Equal(t, w.codec, g.codec, "%s frame %d codec", label, i)
		assert.Equal(t, w.flags, g.flags, "%s frame %d flags", label, i)
		assert.Equal(t, w.channelID, g.channelID, "%s frame %d channel", label, i)
		if !assert.Len(t, g.payload, len(w.payload), "%s frame %d payload len", label, i) {
			t.Fatalf("payload len mismatch at %s frame %d: want %d got %d", label, i, len(w.payload), len(g.payload))
		}
		assert.Equal(t, w.payload, g.payload, "%s frame %d payload", label, i)
	}
}

// baselineToFrames 将 baselineFrame 转为 *Frame（供 MockSource.Push / 校验构造）。
func baselineToFrames(fs []baselineFrame) []*Frame {
	out := make([]*Frame, 0, len(fs))
	for _, f := range fs {
		out = append(out, &Frame{
			Header: FrameHeader{
				Magic:     FrameMagic,
				Version:   FrameVersion,
				ChannelID: f.channelID,
				FrameType: f.frameType,
				Codec:     f.codec,
				Flags:     f.flags,
				PTS:       f.pts,
				DTS:       f.dts,
			},
			Payload: f.payload,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Test1: MP4Source -> MockPipe
//   MockPipe 发送控制请求（GetStreamInfo/Subscribe），并转发数据面帧；
//   收到的数据与基准数据逐字节比对，覆盖 3 轮 loop。
// ---------------------------------------------------------------------------

func TestDataIntegrity_MP4SourceToMockPipe(t *testing.T) {
	b := loadOnvifBaseline(t)

	src, err := NewMP4Source(&InputConfig{
		ID:   "integrity_src_" + t.Name(),
		Type: "file",
		Path: onvifBaselinePath(),
		Loop: true,
	})
	require.NoError(t, err)

	sink := NewMockSink("integrity_sink")
	pipe := NewMockPipe()
	require.NoError(t, pipe.Bind(src, sink))

	// 加速节流，让 readLoop 快速跑完 3 轮
	src.mu.Lock()
	src.throttleOverride = time.Millisecond
	src.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); src.Stop() }()
	require.NoError(t, src.Start(ctx))

	// 控制面：MockPipe 发送 GetStreamInfo 请求
	sig, err := NewSignal(SignalGetStreamInfo, 1, nil)
	require.NoError(t, err)
	reply, err := pipe.SendSignal(ctx, sig)
	require.NoError(t, err)
	require.True(t, reply.IsReply)
	var resp SubscribeResponse
	require.NoError(t, reply.DecodePayload(&resp))
	require.Len(t, resp.Streams, len(b.streams), "stream info should match baseline")

	// 控制面：Subscribe 请求
	subReq, err := NewSignal(SignalSubscribe, 2, &SubscribeRequest{Channels: []uint8{0, 1}})
	require.NoError(t, err)
	_, err = pipe.SendSignal(ctx, subReq)
	require.NoError(t, err)

	// 数据面：等 3 轮视频 + 音频帧到齐
	targetV, targetA := b.videoTotal(3), b.audioTotal(3)
	waitCond(t, 30*time.Second, func() bool {
		v, a := splitVideoAudio(framesToBaseline(sink.Frames()))
		return len(v) >= targetV && len(a) >= targetA
	}, "3 loops of video+audio frames")

	gotV, gotA := splitVideoAudio(framesToBaseline(sink.Frames()))
	want := b.expandLoops(3)
	wantV, wantA := splitVideoAudio(want)

	compareBaseline(t, "video", gotV, wantV)
	compareBaseline(t, "audio", gotA, wantA)
}

// ---------------------------------------------------------------------------
// Test2: MockSource -> DefaultPipe -> MockSink
//   MockSource 提供基准数据（3 轮 loop），DefaultPipe 转发，MockSink 校验。
//   要求不丢帧（pipe.Dropped()==0）且逐字节一致。
// ---------------------------------------------------------------------------

func TestDataIntegrity_PipeForward(t *testing.T) {
	b := loadOnvifBaseline(t)

	// MockSource 提供基准数据
	src := NewMockSource("integrity_pipe_src", b.streams)
	sink := NewMockSink("integrity_pipe_sink")
	pipe := NewDefaultPipe(2048)
	require.NoError(t, pipe.Bind(src, sink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	want := b.expandLoops(3)
	frames := baselineToFrames(want)

	// 分批推送（带节奏，避免背压丢帧），确保全部入队
	chunk := 100
	for i := 0; i < len(frames); i += chunk {
		end := i + chunk
		if end > len(frames) {
			end = len(frames)
		}
		for _, f := range frames[i:end] {
			src.Push(f)
		}
		time.Sleep(2 * time.Millisecond)
	}

	waitCond(t, 30*time.Second, func() bool {
		return sink.FrameCount() >= len(frames)
	}, "all frames forwarded by pipe")

	assert.Equal(t, int64(len(frames)), pipe.Written(), "pipe should write all frames")
	assert.Equal(t, int64(0), pipe.Dropped(), "pipe must not drop any frame")

	got := framesToBaseline(sink.Frames())
	gotV, gotA := splitVideoAudio(got)
	wantV, wantA := splitVideoAudio(want)
	compareBaseline(t, "pipe video", gotV, wantV)
	compareBaseline(t, "pipe audio", gotA, wantA)
}

// ---------------------------------------------------------------------------
// Test3: MockSource -> MockPipe -> RTSPSink，用 RTSPInput 回读校验
//   自己写的"拉流工具"= RTSPInput，把从 sink 拉到的码流逐字节与基准数据比对。
//   覆盖 3 轮 loop。剔除 RTSPSink 在 reader PLAY 时插入的 SPS/PPS 参数帧。
//
//   传输协议是关键：VLC 默认用 UDP 拉流，UDP 在突发推送时接收缓冲溢出会丢包
//   → 花屏。TCP 有重传不丢包。故：
//     - TCP 回读：要求逐字节一致（0 丢帧）
//     - UDP 回读：按 VLC 同等条件复现丢包，验证"花屏根因 = UDP 丢包而非 sink 数据面损坏"
// ---------------------------------------------------------------------------

func TestDataIntegrity_RTSPSinkRoundTrip(t *testing.T) {
	t.Run("TCP", func(t *testing.T) {
		testRTSPRoundTrip(t, "tcp", 8192, true)
	})
	t.Run("UDP", func(t *testing.T) {
		// 本机回环 UDP 不允许丢包：大写队列 + 大读缓冲 + 逐字节校验
		testRTSPRoundTrip(t, "udp", 8192, true)
	})
}

// testRTSPRoundTrip 执行一次 RTSP 回读完整性测试。
//
//	transport: 回读传输协议 tcp/udp
//	writeQueueSize: server 每 reader RTP 写队列容量；0 用 gortsplib 默认
//	expectBitExact: true=要求逐字节一致；false=UDP 场景允许丢包但必须能回读到数据
//
// 注意：globalRTSPManager 按 addr 缓存 server，测试必须用独立 addr 并
// 在结束后 StopAll，避免 TCP/UDP 场景复用同一 server（导致 UDP 监听缺失）。
func testRTSPRoundTrip(t *testing.T, transport string, writeQueueSize int, expectBitExact bool) {
	t.Helper()
	b := loadOnvifBaseline(t)

	// 独立 RTSP 端口（每个子测试独享，避免复用全局缓存的 server）
	srvAddr := "127.0.0.1:" + freeTCPPort(t)

	// UDP 场景才启用 UDP transport（RTP/RTCP 端口需连续且空闲）
	if transport == "udp" {
		rtpAddr, rtcpAddr := freeUDPPortPair(t)
		globalRTSPManager.ConfigureUDP(rtpAddr, rtcpAddr, "", 0, 0)
	}
	globalRTSPManager.SetWriteQueueSize(writeQueueSize)
	defer globalRTSPManager.StopAll() // 隔离全局 server，避免跨子测试复用
	defer globalRTSPManager.ConfigureUDP("", "", "", 0, 0)

	// 真实 RTSPSink（本地 server 模式）
	sink, err := NewRTSPSink(&OutputConfig{
		ID:   "integrity_rtsp_sink_" + transport,
		Type: "rtsp",
		Mode: "server",
		Addr: srvAddr,
	})
	require.NoError(t, err)
	require.NoError(t, sink.Start(context.Background()))
	defer sink.Stop()
	require.NoError(t, sink.Configure(b.streams))
	require.True(t, sink.ready)

	// MockSource 提供基准数据，MockPipe 转发到 sink
	src := NewMockSource("integrity_rtsp_src", b.streams)
	mockPipe := NewMockPipe()
	require.NoError(t, mockPipe.Bind(src, sink))

	// 自己写的拉流工具：RTSPInput 回读 sink 输出（按 transport 指定协议）
	// UDP 读缓冲需 <= 系统 rmem_max(208KB)，否则 SetsockoptInt 忙等
	input, err := NewRTSPInput(&InputConfig{
		ID:                "integrity_rtsp_input_" + transport,
		Type:              "rtsp",
		URL:               "rtsp://" + sinkListenerAddr(sink) + "/live/" + sink.ID(),
		Transport:         transport,
		UDPReadBufferSize: 200 * 1024,
	})
	require.NoError(t, err)

	recvSink := NewMockSink("integrity_rtsp_recv")
	recvPipe := NewDefaultPipe(2048)
	require.NoError(t, recvPipe.Bind(input, recvSink))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, input.Start(ctx))
	require.NoError(t, recvPipe.Start(ctx))
	defer recvPipe.Stop()
	defer input.Stop()

	// 等 reader 连上并 PLAY（sendParamsToStream 触发）
	waitCond(t, 5*time.Second, func() bool {
		return input.Status() == StreamStatusRunning
	}, "RTSP input running")
	t.Logf("[%s] input status=%s", transport, input.Status())
	time.Sleep(300 * time.Millisecond)

	want := b.expandLoops(3)
	wantV, _ := splitVideoAudio(want)
	frames := baselineToFrames(want)

	// 按帧间 PTS 差节流推送（缩放 RATE=100x 实时），模拟真实推流节奏而非突发。
	// 发送节奏主要靠 sink 帧内分片节流保证贴合时间戳，验证零丢帧、逐字节一致。
	const rateBoost = 100
	prevPTS := map[uint8]int64{}
	prevWall := map[uint8]time.Time{}
	pushedVideo := 0
	for i, f := range frames {
		if prevWall[f.Header.ChannelID].IsZero() {
			prevPTS[f.Header.ChannelID] = f.Header.PTS
			prevWall[f.Header.ChannelID] = time.Now()
		} else {
			dt := f.Header.PTS - prevPTS[f.Header.ChannelID]
			prevPTS[f.Header.ChannelID] = f.Header.PTS
			rate := b.streams[f.Header.ChannelID].ClockRate
			if rate <= 0 {
				rate = 90000
			}
			interval := time.Duration(float64(dt) * float64(time.Second) / float64(rate) / rateBoost)
			if interval > 0 && interval < time.Second {
				next := prevWall[f.Header.ChannelID].Add(interval)
				if w := time.Until(next); w > 0 {
					time.Sleep(w)
				}
			}
			prevWall[f.Header.ChannelID] = time.Now()
		}
		src.Push(f)
		if f.Header.FrameType == FrameTypeVideo {
			pushedVideo++
		}
		if i%500 == 0 {
			t.Logf("[%s] pushed %d/%d", transport, i+1, len(frames))
		}
	}

	// 等回读到足够的视频帧
	targetV := b.videoTotal(3)
	waitCond(t, 30*time.Second, func() bool {
		v, _ := splitVideoAudio(framesToBaseline(recvSink.Frames()))
		return len(v) >= targetV
	}, "3 loops of video frames read back from RTSP sink")

	// 剔除 RTSPSink 插入的 SPS/PPS：新 reader PLAY 时单独发参数集（独立帧），
	// 且关键帧现在前置 SPS/PPS。逐字节对比前需从帧内剥离参数集 NAL，
	// 因为基准数据（MP4 sample）的关键帧本身不含参数集。
	gotV, _ := splitVideoAudio(framesToBaseline(recvSink.Frames()))
	gotV = stripParamFrames(gotV)
	gotV = stripInFrameParamNals(gotV)
	// 基准数据的关键帧不含参数集（MP4 sample 的 SPS/PPS 在 avcC），
	// 两边都剥离参数集 NAL 后逐字节对齐对比。
	wantV = stripInFrameParamNals(wantV)

	if expectBitExact {
		compareBaseline(t, "rtsp roundtrip video ["+transport+"]", gotV, wantV)
	} else {
		// UDP 场景：不要求逐字节一致（UDP 丢包是 VLC 花屏根因），
		// 但必须回读到足够帧证明流存活；若一帧都收不到说明 sink 数据面有问题。
		assert.GreaterOrEqual(t, len(gotV), targetV,
			"UDP readback should still receive frames (VLC 花屏 = UDP 丢包，非 sink 数据损坏)")
		t.Logf("UDP readback: received %d video frames (UDP 丢包预期内，需逐帧对比确认损坏位置)", len(gotV))
	}
}

// TestRTSPSink_AudioSeqIncrements 验证音频 RTP 包序列号逐帧递增。
// 曾修复 bug：writeAudio 未设置 SequenceNumber，所有音频包 Seq=0，
// 接收端（VLC）判定严重乱序/丢包。修复后每帧递增。
func TestRTSPSink_AudioSeqIncrements(t *testing.T) {
	aacConfig := []byte{0x11, 0x90}
	sink := newRTSPTestSink(t)
	defer sink.Stop()

	require.NoError(t, sink.Configure([]StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000,
			CodecConfig: []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xe9, 0x40, 0x50, 0x1e, 0xd0, 0x80,
				0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80}},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 48000, CodecConfig: aacConfig},
	}))
	require.True(t, sink.ready)

	// 写多帧音频，逐帧校验序列号递增
	var prev uint16
	for i := 0; i < 20; i++ {
		f := testFrame(1, FrameTypeAudio, CodecAAC, int64(i)*1024, []byte{0xAA, 0xBB, 0xCC, 0x11})
		require.NoError(t, sink.WriteFrame(f))
		seq := sink.audioSeq
		if i == 0 {
			assert.NotEqual(t, uint16(0), seq, "first audio packet seq must not be 0")
		} else {
			assert.Equal(t, prev+1, seq, "audio seq must increment by 1 (got %d after %d)", seq, prev)
		}
		prev = seq
	}
}

// freeTCPPort 返回一个空闲的 TCP 端口号（用于 RTSP server addr）。
func freeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return strconv.Itoa(port)
}

// freeUDPPortPair 返回一对连续的空闲 UDP 端口（RTP/RTCP 需连续）。
func freeUDPPortPair(t *testing.T) (rtpAddr, rtcpAddr string) {
	t.Helper()
	for i := 0; i < 20; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			continue
		}
		rtpPort := ln.LocalAddr().(*net.UDPAddr).Port
		ln.Close()
		if rtpPort == 65535 {
			continue
		}
		// RTSP/UDP 要求 RTP 端口为偶数、RTCP = RTP+1
		if rtpPort%2 != 0 {
			rtpPort--
		}
		rtpAddr = fmt.Sprintf("127.0.0.1:%d", rtpPort)
		rtcpAddr = fmt.Sprintf("127.0.0.1:%d", rtpPort+1)
		return rtpAddr, rtcpAddr
	}
	t.Fatal("no free UDP port pair found")
	return "", ""
}

// stripParamFrames 剔除仅含参数集（H264 SPS/PPS，H265 VPS/SPS/PPS）的帧，
// 这类帧由 RTSPSink 在 reader PLAY 时插入，不属于基准数据。
func stripParamFrames(fs []baselineFrame) []baselineFrame {
	out := make([]baselineFrame, 0, len(fs))
	for _, f := range fs {
		if isParamOnlyFrame(f.payload, f.codec) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// stripInFrameParamNals 从帧内剥离参数集 NAL（H264 SPS/PPS，H265 VPS/SPS/PPS）。
// RTSPSink 关键帧现在前置参数集；基准数据（MP4 sample）的关键帧不含参数集，
// 逐字节对比前需从帧内剥离参数集 NAL 使两者对齐。
func stripInFrameParamNals(fs []baselineFrame) []baselineFrame {
	out := make([]baselineFrame, 0, len(fs))
	for _, f := range fs {
		nals := splitAnnexB(f.payload)
		kept := make([][]byte, 0, len(nals))
		for _, nal := range nals {
			if len(nal) == 0 {
				continue
			}
			switch f.codec {
			case CodecH265:
				if len(nal) >= 2 {
					nt := (nal[0] >> 1) & 0x3F
					if nt == 32 || nt == 33 || nt == 34 {
						continue
					}
				}
			default: // H264
				nt := nal[0] & 0x1F
				if nt == 7 || nt == 8 {
					continue
				}
			}
			kept = append(kept, nal)
		}
		if len(kept) != len(nals) {
			f.payload = joinNals(kept)
		}
		out = append(out, f)
	}
	return out
}

// isParamOnlyFrame 判断帧是否只含参数集 NAL。
func isParamOnlyFrame(payload []byte, codec CodecID) bool {
	nals := splitAnnexB(payload)
	if len(nals) == 0 {
		return false
	}
	for _, nal := range nals {
		if len(nal) == 0 {
			continue
		}
		switch codec {
		case CodecH265:
			if len(nal) < 2 {
				return false
			}
			nt := (nal[0] >> 1) & 0x3F
			if nt != 32 && nt != 33 && nt != 34 {
				return false
			}
		default: // H264
			nt := nal[0] & 0x1F
			if nt != 7 && nt != 8 {
				return false
			}
		}
	}
	return true
}
