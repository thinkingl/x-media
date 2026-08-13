package media

import (
	"encoding/binary"
)

// FLV 封装器：将标准帧编码为 FLV tag 流（HTTP-FLV 与 RTMP 共用）。
//
// FLV 时间戳单位为毫秒：tagTime = PTS * 1000 / ClockRate。
// H.264 在 FLV 中的 AVCC 封装用 composition time offset（CTS = PTS - DTS）。

const (
	flvCodecH264     = 7
	flvCodecAAC      = 10
	flvAVCPacketTypeSeq = 0 // 视频 sequence header
	flvAVCPacketTypeNALU = 1
	flvAACPacketTypeSeq  = 0 // 音频 sequence header
	flvAACPacketTypeRaw  = 1
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
}

// NewFLVWriter 创建 FLV writer。
func NewFLVWriter() *FLVWriter {
	return &FLVWriter{clocks: make(map[uint8]int)}
}

// SetClockRate 注册子通道时钟率（由 Configure 调用）。
func (w *FLVWriter) SetClockRate(channelID uint8, rate int) {
	w.clocks[channelID] = rate
}

// Reset 重置状态（如循环时重新开始）。
func (w *FLVWriter) Reset() {
	w.started = false
	w.hasVideo = false
	w.hasAudio = false
	w.clocks = make(map[uint8]int)
}

// Header 返回 FLV 文件头（应最先发送）。
func (w *FLVWriter) Header() []byte {
	return flvHeader
}

// BuildConfigTags 从 StreamInfo 生成 sequence header tag（flv header 之后发送）。
// 返回按子通道顺序的 tag 字节流。
func (w *FLVWriter) BuildConfigTags(streams []StreamInfo) []byte {
	var out []byte
	for _, s := range streams {
		switch s.Kind {
		case "video":
			if s.CodecConfig != nil {
				out = append(out, w.videoSequenceHeader(s.CodecConfig)...)
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
		return w.videoTag(f)
	case FrameTypeAudio:
		return w.audioTag(f)
	}
	return nil
}

// EncodeTagData 将标准帧编码为 RTMP 消息体（FLV tag 的 data 部分）。
// 返回消息类型(8=audio/9=video)与毫秒时间戳。供 RTMP sink 使用。
func (w *FLVWriter) EncodeTagData(f *Frame) (tagType byte, ts uint32, data []byte) {
	switch f.Header.FrameType {
	case FrameTypeVideo:
		cts := f.Header.PTS - f.Header.DTS
		if cts < 0 {
			cts = 0
		}
		avcc := annexBToAVCC(f.Payload)
		ft := byte(0x02)
		if f.Header.Flags&FlagKeyframe != 0 {
			ft = 0x01
		}
		data = make([]byte, 0, 5+len(avcc))
		data = append(data, ft<<4|flvCodecH264)
		data = append(data, flvAVCPacketTypeNALU)
		data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
		data = append(data, avcc...)
		return 9, uint32(ToMilliseconds(f.Header.DTS, w.clockOf(f.Header.ChannelID, 90000))), data
	case FrameTypeAudio:
		data = make([]byte, 0, 2+len(f.Payload))
		data = append(data, 0xAF)
		data = append(data, flvAACPacketTypeRaw)
		data = append(data, f.Payload...)
		return 8, uint32(ToMilliseconds(f.Header.PTS, w.clockOf(f.Header.ChannelID, 0))), data
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

	// 构造 AVCC length-prefixed 数据（从 AnnexB 转回）
	avcc := annexBToAVCC(f.Payload)

	ft := byte(0x02) // inter frame
	if f.Header.Flags&FlagKeyframe != 0 {
		ft = 0x01 // key frame
	}

	data := make([]byte, 0, 5+len(avcc))
	data = append(data, ft<<4|flvCodecH264)
	data = append(data, flvAVCPacketTypeNALU)
	data = append(data, byte(cts>>16), byte(cts>>8), byte(cts))
	data = append(data, avcc...)

	ts := uint32(ToMilliseconds(f.Header.DTS, w.clockOf(f.Header.ChannelID, 90000)))
	return w.buildTag(9, ts, data)
}

// audioTag 编码音频 tag。
func (w *FLVWriter) audioTag(f *Frame) []byte {
	data := make([]byte, 0, 2+len(f.Payload))
	data = append(data, 0xAF) // AAC 44k mono (实际声道数由 config 决定)
	data = append(data, flvAACPacketTypeRaw)
	data = append(data, f.Payload...)

	ts := uint32(ToMilliseconds(f.Header.PTS, w.clockOf(f.Header.ChannelID, 0)))
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
