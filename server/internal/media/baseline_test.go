package media

// 数据完整性测试的共享基准数据模块。
//
// 从指定 mp4 文件中抽取全部帧（视频 AnnexB + 音频原始 AAC），作为
// mock source / mock pipe / mock sink 共用的基准数据（golden data）。
// 支持多轮 loop：loopDur 按视频基准轨道总时长累加，与 MP4Source.alignLoopDuration
// 的换算逻辑保持一致（音频 loopDur = 基准绝对时长换算到音频 timescale）。
//
// 后续切换数据源时只需替换 loadBaseline 的实现（如从 RTSP/抓包文件抽取）。

import (
	"os"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// baselineFrame 单帧基准数据：只保留参与逐字节校验所需的字段。
type baselineFrame struct {
	channelID uint8
	frameType FrameType
	codec     CodecID
	flags     FrameFlag // 关键帧标志等
	pts       int64     // 原始 pts（无 loopDur 偏移）
	dts       int64
	payload   []byte // 视频: AnnexB；音频: 原始 AAC
}

// baseline 一整套基准数据（video/audio 分离，便于按轨道校验）。
type baseline struct {
	streams   []StreamInfo
	video     []baselineFrame // 按 sampleNr 升序
	audio     []baselineFrame
	videoRate int // 视频 timescale
	audioRate int // 音频 timescale
	totalDur  int64
}

// loopOffset 返回第 k 轮（k 从 0 计）的基准绝对偏移（视频 timescale）。
func (b *baseline) loopOffset(k int) int64 { return b.totalDur * int64(k) }

// expandLoops 将基准数据展开为 loops 轮（带 loopDur 偏移）。
// 视频与音频各自保持原顺序与完整性（每轮全部帧出现一次），
// 对比时按轨道拆分校验，故无需严格复刻 readLoop 的交错顺序。
func (b *baseline) expandLoops(loops int) []baselineFrame {
	out := make([]baselineFrame, 0, len(b.video)*loops+len(b.audio)*loops)
	for k := 0; k < loops; k++ {
		vOff := b.loopOffset(k)
		aOff := ConvertClock(vOff, b.videoRate, b.audioRate)
		for _, f := range b.video {
			f.pts += vOff
			f.dts += vOff
			out = append(out, f)
		}
		for _, f := range b.audio {
			f.pts += aOff
			f.dts += aOff
			out = append(out, f)
		}
	}
	return out
}

// videoTotal 展开后视频帧总数。
func (b *baseline) videoTotal(loops int) int { return len(b.video) * loops }

// audioTotal 展开后音频帧总数。
func (b *baseline) audioTotal(loops int) int { return len(b.audio) * loops }

// framesToBaseline 将 MockSink 收到的 *Frame 转为可比较的 baselineFrame。
func framesToBaseline(frames []*Frame) []baselineFrame {
	out := make([]baselineFrame, 0, len(frames))
	for _, f := range frames {
		out = append(out, baselineFrame{
			channelID: f.Header.ChannelID,
			frameType: f.Header.FrameType,
			codec:     f.Header.Codec,
			flags:     f.Header.Flags,
			pts:       f.Header.PTS,
			dts:       f.Header.DTS,
			payload:   f.Payload,
		})
	}
	return out
}

// split 将展开帧流按视频/音频分离（保留各自顺序）。
func splitVideoAudio(frames []baselineFrame) (video, audio []baselineFrame) {
	for _, f := range frames {
		if f.frameType == FrameTypeVideo {
			video = append(video, f)
		} else {
			audio = append(audio, f)
		}
	}
	return
}

// loadBaseline 从 mp4 文件抽取基准数据。
// 视频 payload 转 AnnexB（与 MP4Source 输出一致），音频保留原始 AAC。
// 单圈时长 = 基准轨道 stts 全量求和（与 mp4Track.totalDuration 一致）。
func loadBaseline(t *testing.T, path string) *baseline {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err, "open baseline mp4")
	defer f.Close()

	mp4f, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	require.NoError(t, err, "decode baseline mp4")
	require.False(t, mp4f.IsFragmented(), "fragmented mp4 not supported as baseline")

	var b baseline
	var raw []baselineFrame
	channelID := uint8(0)

	for _, trak := range mp4f.Moov.Traks {
		if trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil {
			continue
		}
		stbl := trak.Mdia.Minf.Stbl
		timescale := 90000
		if trak.Mdia.Mdhd != nil {
			timescale = int(trak.Mdia.Mdhd.Timescale)
		}

		var isVideo bool
		var codec CodecID
		info := StreamInfo{ChannelID: channelID}
		switch trak.Mdia.Hdlr.HandlerType {
		case "vide":
			isVideo = true
			info.Kind = "video"
			if stbl.Stsd != nil && stbl.Stsd.AvcX != nil {
				codec = CodecH264
				info.CodecName = "H264"
				info.Parameters = map[string]any{
					"width":  int(stbl.Stsd.AvcX.Width),
					"height": int(stbl.Stsd.AvcX.Height),
				}
				if stbl.Stsd.AvcX.AvcC != nil {
					info.CodecConfig = extractAVCDecConfRec(&stbl.Stsd.AvcX.AvcC.DecConfRec)
				}
			} else if stbl.Stsd != nil && stbl.Stsd.HvcX != nil {
				codec = CodecH265
				info.CodecName = "H265"
				info.Parameters = map[string]any{
					"width":  int(stbl.Stsd.HvcX.Width),
					"height": int(stbl.Stsd.HvcX.Height),
				}
				if stbl.Stsd.HvcX.HvcC != nil {
					info.CodecConfig = extractHvcCDecConfRec(&stbl.Stsd.HvcX.HvcC.DecConfRec)
				}
			} else {
				continue
			}
		case "soun":
			isVideo = false
			info.Kind = "audio"
			codec = CodecAAC
			info.CodecName = "AAC"
			if stbl.Stsd != nil && stbl.Stsd.Mp4a != nil {
				a := stbl.Stsd.Mp4a
				info.Parameters = map[string]any{
					"channels":    int(a.ChannelCount),
					"sample_rate": int(a.SampleRate),
				}
				if a.Esds != nil && a.Esds.DecConfigDescriptor != nil && a.Esds.DecConfigDescriptor.DecSpecificInfo != nil {
					info.CodecConfig = a.Esds.DecConfigDescriptor.DecSpecificInfo.DecConfig
				}
			}
		default:
			continue
		}
		info.CodecID = codec
		info.ClockRate = timescale
		b.streams = append(b.streams, info)

		total := int(trak.GetNrSamples())
		ws := make([]byte, 0, 512*1024)
		for nr := 1; nr <= total; nr++ {
			stts := stbl.Stts
			dts, _ := stts.GetDecodeTime(uint32(nr))
			pts := int64(dts)
			if isVideo {
				if ctts := stbl.Ctts; ctts != nil {
					pts = int64(int64(dts) + int64(ctts.GetCompositionTimeOffset(uint32(nr))))
				}
			}
			isKey := false
			if isVideo && stbl.Stss != nil {
				isKey = stbl.Stss.IsSyncSample(uint32(nr))
			}

			w := newBytesWriter()
			require.NoError(t, mp4f.CopySampleData(w, f, trak, uint32(nr), uint32(nr), ws), "copy sample %d", nr)
			data := w.Bytes()
			if isVideo {
				data = avccToAnnexB(data)
			}

			ft := FrameTypeAudio
			var flags FrameFlag
			if isVideo {
				ft = FrameTypeVideo
				flags = frameFlags(isKey, false)
			}
			raw = append(raw, baselineFrame{
				channelID: channelID,
				frameType: ft,
				codec:     codec,
				flags:     flags,
				pts:       pts,
				dts:       int64(dts),
				payload:   data,
			})
		}
		channelID++
	}

	require.NotEmpty(t, raw, "no media frames extracted from baseline")
	for _, f := range raw {
		if f.frameType == FrameTypeVideo {
			b.video = append(b.video, f)
		} else {
			b.audio = append(b.audio, f)
		}
	}
	require.NotEmpty(t, b.video, "no video frames")
	require.NotEmpty(t, b.audio, "no audio frames")

	// 视频基准轨道总时长（视频 timescale）：stts 全量求和
	videoDur := int64(0)
	for _, trak := range mp4f.Moov.Traks {
		if trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil ||
			trak.Mdia.Hdlr == nil || trak.Mdia.Hdlr.HandlerType != "vide" {
			continue
		}
		stts := trak.Mdia.Minf.Stbl.Stts
		if stts == nil {
			continue
		}
		for i := range stts.SampleCount {
			videoDur += int64(stts.SampleCount[i]) * int64(stts.SampleTimeDelta[i])
		}
		break
	}
	b.totalDur = videoDur

	b.videoRate = b.streams[0].ClockRate
	if b.videoRate <= 0 {
		b.videoRate = 90000
	}
	if len(b.streams) > 1 {
		b.audioRate = b.streams[1].ClockRate
	}
	if b.audioRate <= 0 {
		b.audioRate = 32000
	}
	return &b
}
