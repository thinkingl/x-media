package media

import (
	"bytes"
	"encoding/binary"

	"github.com/Eyevinn/mp4ff/hevc"
)

// FLV 封装器：将标准帧编码为 FLV tag 流（HTTP-FLV 与 RTMP 共用）。
//
// FLV 时间戳单位为毫秒：tagTime = PTS * 1000 / ClockRate。
// H.264 在 FLV 中的 AVCC 封装用 composition time offset（CTS = PTS - DTS）。

const (
	flvCodecH264        = 7
	flvCodecAAC         = 10
	flvAVCPacketTypeSeq    = 0 // 视频 sequence header
	flvAVCPacketTypeNALU   = 1
	flvAACPacketTypeSeq    = 0 // 音频 sequence header
	flvAACPacketTypeRaw    = 1

	// onMetaData 中 videocodecid 的约定值（ffmpeg 使用）：
	// H264=7，HEVC=120（ffmpeg 的 FLV_CODECID_HEVC 映射）。
	flvMetaVideoCodecHEVC = 120

	// Enhanced RTMP / ExVideoTagHeader（HEVC 等）：字节0 为
	// FLV_IS_EX_HEADER | packetType | frametype，随后 4 字节 fourcc("hvc1")。
	// 常量与 FFmpeg libavformat/flv.h 保持一致，保证 ffmpeg/VLC 可解。
	flvIsExHeader         = 0x80
	flvExPacketTypeSeq    = 0 // PacketTypeSequenceStart
	flvExPacketTypeFrames = 1 // PacketTypeCodedFrames（带 CTS）
	flvExPacketTypeFramesX = 3 // PacketTypeCodedFramesX（PTS==DTS，无 CTS）
	flvExFrameKey         = 0x10 // FLV_FRAME_KEY = 1<<4
	flvExFrameInter       = 0x20 // FLV_FRAME_INTER = 2<<4
)

var flvHeader = []byte{
	0x46, 0x4C, 0x56, 0x01, // 'FLV', version 1
	0x01,                   // audio+video flags
	0x00, 0x00, 0x00, 0x09, // header size
	0x00, 0x00, 0x00, 0x00, // previous tag size 0
}

// FLVWriter 将标准帧写入 FLV tag 流。
type FLVWriter struct {
	started  bool
	hasVideo bool
	hasAudio bool
	clocks   map[uint8]int // ChannelID → ClockRate

	// 单调流时钟：FLV tag 时间戳必须是单调递增的相对时钟。
	// 源帧 DTS 是绝对媒体时间（含 loopDur 累计），loop 回绕时会跳回小值，
	// 直接用它会导致播放器时间轴错乱/帧率失控。这里改为**累加帧间 delta**：
	//   videoAcc += max(0, curDTS - prevDTS)
	// 回绕时 delta<0 被钳制为 0（时间戳暂停不后退），后续正常递增，天然单调。
	videoAcc    int64 // 视频累积时间戳（流内 timescale）
	prevVideoDTS int64
	hasPrevVideo bool
	// audioAcc 同理（音频用 PTS）。
	audioAcc    int64
	prevAudioPTS int64
	hasPrevAudio bool

	// HEVC 参数集缓存（从 CodecConfig 提取，Configure 时填充）。
	// 数据关键帧编码时前置 VPS/SPS/PPS，保证任意时刻接入/丢帧后都可解码。
	hevcVPS []byte
	hevcSPS []byte
	hevcPPS []byte

	// H264 参数集缓存（从 CodecConfig 提取，Configure 时填充）。
	// 数据关键帧编码时前置 SPS/PPS，保证任意 GOP 起点接入都可解码。
	h264SPS []byte
	h264PPS []byte
}

// NewFLVWriter 创建 FLV writer。
func NewFLVWriter() *FLVWriter {
	return &FLVWriter{clocks: make(map[uint8]int)}
}

// Reset 重置状态（如循环时重新开始）。
func (w *FLVWriter) Reset() {
	w.started = false
	w.hasVideo = false
	w.hasAudio = false
	w.clocks = make(map[uint8]int)
	w.videoAcc = 0
	w.prevVideoDTS = 0
	w.hasPrevVideo = false
	w.audioAcc = 0
	w.prevAudioPTS = 0
	w.hasPrevAudio = false
	w.hevcVPS = nil
	w.hevcSPS = nil
	w.hevcPPS = nil
	w.h264SPS = nil
	w.h264PPS = nil
}

// nextVideoTS 推进视频累积时钟并返回当前视频时间戳（流内 timescale）。
func (w *FLVWriter) nextVideoTS(dts int64) int64 {
	if !w.hasPrevVideo {
		w.prevVideoDTS = dts
		w.hasPrevVideo = true
		w.videoAcc = 0
		return 0
	}
	delta := dts - w.prevVideoDTS
	w.prevVideoDTS = dts
	if delta > 0 && delta < int64(1<<40) {
		w.videoAcc += delta
	}
	return w.videoAcc
}

// nextAudioTS 推进音频累积时钟并返回当前音频时间戳（流内 timescale）。
func (w *FLVWriter) nextAudioTS(pts int64) int64 {
	if !w.hasPrevAudio {
		w.prevAudioPTS = pts
		w.hasPrevAudio = true
		w.audioAcc = 0
		return 0
	}
	delta := pts - w.prevAudioPTS
	w.prevAudioPTS = pts
	if delta > 0 && delta < int64(1<<40) {
		w.audioAcc += delta
	}
	return w.audioAcc
}

// SetClockRate 注册子通道时钟率（由 Configure 调用）。
func (w *FLVWriter) SetClockRate(channelID uint8, rate int) {
	w.clocks[channelID] = rate
}

// Header 返回 FLV 文件头（应最先发送）。
func (w *FLVWriter) Header() []byte {
	return flvHeader
}

// BuildOnMetaDataTag 生成 onMetaData tag（type=18，AMF0）。
// ffmpeg/VLC 的 flv demuxer 依赖 onMetaData 中的时长/编解码信息来建立
// 正确的 dts 时间轴；缺失时走启发式，对无 seek 索引的 HTTP-FLV 会重复帧/卡顿。
// 直播流 duration=0，不带 keyframes 索引。
func (w *FLVWriter) BuildOnMetaDataTag(streams []StreamInfo) []byte {
	meta := map[string]any{
		"duration":     0.0,
		"width":        0.0,
		"height":       0.0,
		"framerate":    0.0,
		"videocodecid": float64(flvCodecH264),
		"audiocodecid": float64(flvCodecAAC),
		"encoder":      "x-media",
	}
	for _, s := range streams {
		switch s.Kind {
		case "video":
			if s.CodecID == CodecH265 {
				meta["videocodecid"] = float64(flvMetaVideoCodecHEVC)
			}
			if v, ok := s.Parameters["width"].(int); ok {
				meta["width"] = float64(v)
			}
			if v, ok := s.Parameters["height"].(int); ok {
				meta["height"] = float64(v)
			}
			if v, ok := s.Parameters["fps"].(float64); ok {
				meta["framerate"] = v
			}
		case "audio":
			if v, ok := s.Parameters["sample_rate"].(int); ok {
				meta["audiosamplerate"] = float64(v)
			}
			if v, ok := s.Parameters["channels"].(int); ok {
				meta["audiochannels"] = float64(v)
			}
		}
	}

	payload := make([]byte, 0, 64)
	payload = append(payload, amf0String("onMetaData")...)
	payload = append(payload, encodeAMF0Metadata(meta)...)
	return w.buildTag(18, 0, payload)
}

// encodeAMF0Metadata 按 ffmpeg 兼容的 AMF0 strict array(0x08) 编码 onMetaData 对象。
// strict array 用长度字段确定元素数量，不需要 object end marker（0x00 0x00 0x09）；
// 加结束标记会被 ffmpeg 当作下一个 AMF 值误读（如 type 216）。
func encodeAMF0Metadata(m map[string]any) []byte {
	b := []byte{0x08} // AMF0 strict array
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(m)))
	b = append(b, lenBuf[:]...)
	for k, v := range m {
		b = append(b, amf0String(k)...)
		vb, err := encodeAMF0(v)
		if err != nil {
			continue
		}
		b = append(b, vb...)
	}
	return b
}

// BuildConfigTags 从 StreamInfo 生成 sequence header tag（flv header 之后发送）。
// 返回按子通道顺序的 tag 字节流。
func (w *FLVWriter) BuildConfigTags(streams []StreamInfo) []byte {
	var out []byte
	for _, s := range streams {
		switch s.Kind {
		case "video":
			if s.CodecConfig != nil {
				if s.CodecID == CodecH265 {
					vps, sps, pps := splitCodecConfigHevc(s.CodecConfig)
					w.hevcVPS = vps
					w.hevcSPS = sps
					w.hevcPPS = pps
					out = append(out, w.hevcSequenceHeader(s.CodecConfig)...)
				} else {
					sps, pps := splitCodecConfigVideo(s.CodecConfig)
					w.h264SPS = sps
					w.h264PPS = pps
					out = append(out, w.videoSequenceHeader(s.CodecConfig)...)
				}
				w.hasVideo = true
			}
		case "audio":
			if s.CodecConfig != nil {
				out = append(out, w.audioSequenceHeader(s.CodecConfig)...)
				w.hasAudio = true
			}
		}
	}
	w.started = true
	return out
}

// EncodeTag 将一个标准帧编码为 FLV tag。
func (w *FLVWriter) EncodeTag(f *Frame) []byte {
	switch f.Header.FrameType {
	case FrameTypeVideo:
		w.nextVideoTS(f.Header.DTS)
		return w.videoTag(f)
	case FrameTypeAudio:
		w.nextAudioTS(f.Header.PTS)
		return w.audioTag(f)
	}
	return nil
}

// EncodeTagData 将标准帧编码为 RTMP 消息体（FLV tag 的 data 部分）。
// 返回消息类型(8=audio/9=video)与毫秒时间戳。供 RTMP sink 使用。
func (w *FLVWriter) EncodeTagData(f *Frame) (tagType byte, ts uint32, data []byte) {
	switch f.Header.FrameType {
	case FrameTypeVideo:
		videoTS := w.nextVideoTS(f.Header.DTS)
		cts := f.Header.PTS - f.Header.DTS
		if cts < 0 {
			cts = 0
		}
		ft := byte(0x02)
		if f.Header.Flags&FlagKeyframe != 0 {
			ft = 0x01
		}
		if f.Header.Codec == CodecH265 {
			ft := byte(flvExFrameInter)
			if f.Header.Flags&FlagKeyframe != 0 {
				ft = flvExFrameKey
			}
			hvcc := annexBToAVCC(f.Payload)
			if f.Header.Flags&FlagKeyframe != 0 {
				hvcc = prependHevcParams(hvcc, w.hevcVPS, w.hevcSPS, w.hevcPPS)
			}
			data := make([]byte, 0, 5+len(hvcc))
			if cts > 0 {
				data = append(data, flvIsExHeader|flvExPacketTypeFrames|ft)
				data = append(data, 'h', 'v', 'c', '1')
				data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
			} else {
				data = append(data, flvIsExHeader|flvExPacketTypeFramesX|ft)
				data = append(data, 'h', 'v', 'c', '1')
			}
			data = append(data, hvcc...)
			return 9, uint32(ToMilliseconds(videoTS, w.clockOf(f.Header.ChannelID, 90000))), data
		}
		avcc := annexBToAVCC(f.Payload)
		if f.Header.Flags&FlagKeyframe != 0 {
			avcc = prependH264Params(avcc, w.h264SPS, w.h264PPS)
		}
		data := make([]byte, 0, 5+len(avcc))
		data = append(data, ft<<4|flvCodecH264)
		data = append(data, flvAVCPacketTypeNALU)
		data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
		data = append(data, avcc...)
		return 9, uint32(ToMilliseconds(videoTS, w.clockOf(f.Header.ChannelID, 90000))), data
	case FrameTypeAudio:
		audioTS := w.nextAudioTS(f.Header.PTS)
		data = make([]byte, 0, 2+len(f.Payload))
		data = append(data, 0xAF)
		data = append(data, flvAACPacketTypeRaw)
		data = append(data, f.Payload...)
		return 8, uint32(ToMilliseconds(audioTS, w.clockOf(f.Header.ChannelID, 0))), data
	}
	return 0, 0, nil
}

// videoSequenceHeader 从 CodecConfig(AnnexB SPS/PPS) 生成 AVCDecoderConfigurationRecord。
func (w *FLVWriter) videoSequenceHeader(config []byte) []byte {
	sps, pps := splitCodecConfigVideo(config)

	// AVCDecoderConfigurationRecord
	avcC := make([]byte, 0, 32)
	avcC = append(avcC, 0x01, 0x64, 0x00, 0x1f, 0xff) // version, profile, compat, level, 0xfc|3
	avcC = append(avcC, 0xe1)                           // lengthSizeMinusOne=3, numSPS=1
	avcC = append(avcC, byte(len(sps)>>8), byte(len(sps)))
	avcC = append(avcC, sps...)
	avcC = append(avcC, 0x01) // numPPS
	avcC = append(avcC, byte(len(pps)>>8), byte(len(pps)))
	avcC = append(avcC, pps...)

	data := make([]byte, 0, 5+len(avcC))
	data = append(data, 0x17) // frameType=1(key) | codecID=7(H264)
	data = append(data, flvAVCPacketTypeSeq)
	data = append(data, 0x00, 0x00, 0x00) // CTS=0
	data = append(data, avcC...)
	return w.buildTag(9, 0, data)
}

// hevcSequenceHeader 生成 HEVC 的 sequence header（Enhanced RTMP / ExVideoTagHeader）。
// 格式与 FFmpeg flvenc.c 一致：0x90(IS_EX|SeqStart|KEY) + "hvc1" + HEVCDecoderConfigurationRecord。
func (w *FLVWriter) hevcSequenceHeader(config []byte) []byte {
	vps, sps, pps := splitCodecConfigHevc(config)
	if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
		return nil
	}

	hvcC := buildHVCC(vps, sps, pps)

	data := make([]byte, 0, 5+len(hvcC))
	data = append(data, flvIsExHeader|flvExPacketTypeSeq|flvExFrameKey) // 0x90
	data = append(data, 'h', 'v', 'c', '1')
	data = append(data, hvcC...)
	return w.buildTag(9, 0, data)
}

// buildHVCC 构造 HEVCDecoderConfigurationRecord（ISO/IEC 14496-15）。
// 使用 mp4ff 解析 SPS 提取 profile/level/尺寸等信息，生成标准 hvcC，
// 保证 ffmpeg/VLC 能正确初始化 HEVC 解码器。
func buildHVCC(vps, sps, pps []byte) []byte {
	rec, err := hevc.CreateHEVCDecConfRec(
		[][]byte{vps}, [][]byte{sps}, [][]byte{pps},
		true, true, true, true,
	)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := rec.Encode(&buf); err != nil {
		return nil
	}
	return buf.Bytes()
}

// audioSequenceHeader 从 AudioSpecificConfig 生成 AAC sequence header。
func (w *FLVWriter) audioSequenceHeader(config []byte) []byte {
	data := make([]byte, 0, 2+len(config))
	data = append(data, 0xAF) // soundFormat=10(AAC), soundRate=3(44k), size=1, type=1(mono)
	data = append(data, flvAACPacketTypeSeq)
	data = append(data, config...)
	return w.buildTag(8, 0, data)
}

// videoTag 编码视频 tag。
func (w *FLVWriter) videoTag(f *Frame) []byte {
	cts := f.Header.PTS - f.Header.DTS
	if cts < 0 {
		cts = 0
	}

	if f.Header.Codec == CodecH265 {
		return w.buildVideoTag(f, cts)
	}

	// 构造 AVCC length-prefixed 数据（从 AnnexB 转回）
	avcc := annexBToAVCC(f.Payload)

	// 关键帧前置 SPS/PPS（length-prefixed），保证任意 GOP 起点接入都可解码。
	// VLC/ffmpeg 探测阶段可能消费掉 seq header，正式播放从 ring 的 GOP 起点开始，
	// 若关键帧不含参数集则解码器无法初始化 → 花屏。
	if f.Header.Flags&FlagKeyframe != 0 {
		avcc = prependH264Params(avcc, w.h264SPS, w.h264PPS)
	}

	ft := byte(0x02) // inter frame
	if f.Header.Flags&FlagKeyframe != 0 {
		ft = 0x01 // key frame
	}

	data := make([]byte, 0, 5+len(avcc))
	data = append(data, ft<<4|flvCodecH264)
	data = append(data, flvAVCPacketTypeNALU)
	data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
	data = append(data, avcc...)

	ts := uint32(ToMilliseconds(w.videoAcc, w.clockOf(f.Header.ChannelID, 90000)))
	return w.buildTag(9, ts, data)
}

// prependH264Params 在关键帧数据前插入 SPS/PPS（以 4 字节长度前缀形式）。
func prependH264Params(avcc, sps, pps []byte) []byte {
	params := make([]byte, 0, len(sps)+len(pps)+8)
	for _, n := range [][]byte{sps, pps} {
		if len(n) == 0 {
			continue
		}
		params = append(params, byte(len(n)>>24), byte(len(n)>>16), byte(len(n)>>8), byte(len(n)))
		params = append(params, n...)
	}
	if len(params) == 0 {
		return avcc
	}
	out := make([]byte, 0, len(params)+len(avcc))
	out = append(out, params...)
	return append(out, avcc...)
}

// buildVideoTag 编码 H.265(HEVC) 视频 tag（Enhanced RTMP / ExVideoTagHeader）。
// 格式与 FFmpeg flvenc.c 一致：
//
//	关键帧: 0x91(IS_EX|CodedFrames|KEY) + "hvc1" + cts(3B) + length-prefixed 数据
//	普通帧: 0xA3(IS_EX|CodedFramesX|INTER) + "hvc1" + length-prefixed 数据（PTS==DTS 无 CTS）
func (w *FLVWriter) buildVideoTag(f *Frame, cts int64) []byte {
	ft := byte(flvExFrameInter)
	if f.Header.Flags&FlagKeyframe != 0 {
		ft = flvExFrameKey
	}

	// HEVC 在 Enhanced RTMP 中数据为 length-prefixed（4 字节 NAL 长度），与 H264 AVCC 一致
	hvcc := annexBToAVCC(f.Payload)

	// 关键帧前置 VPS/SPS/PPS（length-prefixed），保证任意时刻接入/丢帧后都可解码。
	if f.Header.Flags&FlagKeyframe != 0 {
		hvcc = prependHevcParams(hvcc, w.hevcVPS, w.hevcSPS, w.hevcPPS)
	}

	data := make([]byte, 0, 5+len(hvcc))
	// PTS != DTS 时用 CodedFrames 带 CTS，否则用 CodedFramesX 不带
	if cts > 0 {
		data = append(data, flvIsExHeader|flvExPacketTypeFrames|ft)
		data = append(data, 'h', 'v', 'c', '1')
		data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
	} else {
		data = append(data, flvIsExHeader|flvExPacketTypeFramesX|ft)
		data = append(data, 'h', 'v', 'c', '1')
	}
	data = append(data, hvcc...)

	ts := uint32(ToMilliseconds(w.videoAcc, w.clockOf(f.Header.ChannelID, 90000)))
	return w.buildTag(9, ts, data)
}

// prependHevcParams 在关键帧数据前插入 VPS/SPS/PPS（以 4 字节长度前缀形式）。
func prependHevcParams(avcc []byte, vps, sps, pps []byte) []byte {
	params := make([]byte, 0, len(vps)+len(sps)+len(pps)+12)
	for _, n := range [][]byte{vps, sps, pps} {
		if len(n) == 0 {
			continue
		}
		params = append(params, byte(len(n)>>24), byte(len(n)>>16), byte(len(n)>>8), byte(len(n)))
		params = append(params, n...)
	}
	if len(params) == 0 {
		return avcc
	}
	out := make([]byte, 0, len(params)+len(avcc))
	out = append(out, params...)
	return append(out, avcc...)
}

// audioTag 编码音频 tag。
func (w *FLVWriter) audioTag(f *Frame) []byte {
	data := make([]byte, 0, 2+len(f.Payload))
	data = append(data, 0xAF) // AAC 44k mono (实际声道数由 config 决定)
	data = append(data, flvAACPacketTypeRaw)
	data = append(data, f.Payload...)

	ts := uint32(ToMilliseconds(w.audioAcc, w.clockOf(f.Header.ChannelID, 0)))
	return w.buildTag(8, ts, data)
}

// clockOf 返回子通道时钟率，未注册时用默认值。
func (w *FLVWriter) clockOf(channelID uint8, fallback int) int {
	if rate, ok := w.clocks[channelID]; ok && rate > 0 {
		return rate
	}
	if fallback > 0 {
		return fallback
	}
	return 1000 // ms 直接映射
}

// buildTag 组装 FLV tag + previous tag size。
func (w *FLVWriter) buildTag(tagType byte, ts uint32, data []byte) []byte {
	size := len(data)
	tag := make([]byte, 11+size+4)

	tag[0] = tagType
	tag[1] = byte(size >> 16)
	tag[2] = byte(size >> 8)
	tag[3] = byte(size)
	// timestamp 24bit + ext 8bit
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
	// streamID = 0
	copy(tag[8:11], []byte{0x00, 0x00, 0x00})

	copy(tag[11:], data)

	// previous tag size（含 11 字节头 + data）
	prev := uint32(11 + size)
	binary.BigEndian.PutUint32(tag[11+size:], prev)
	return tag
}

// annexBToAVCC 将 AnnexB(start code) 转 AVCC(4-byte length)。
func annexBToAVCC(data []byte) []byte {
	var out []byte
	i := 0
	for i < len(data) {
		// 找 start code
		if i+3 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			i += 3
		} else if i+4 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			i += 4
		} else {
			break
		}
		// 找 NAL 末尾（下一个 start code）
		start := i
		for i < len(data) {
			if i+3 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
				break
			}
			if i+4 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
				break
			}
			i++
		}
		nal := data[start:i]
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nal)))
		out = append(out, lenBuf[:]...)
		out = append(out, nal...)
	}
	return out
}
