package media

import (
	"fmt"
)

type CodecID uint32

const (
	CodecH264    CodecID = 27
	CodecH265    CodecID = 173
	CodecVP8     CodecID = 139
	CodecVP9     CodecID = 167
	CodecAV1     CodecID = 225
	CodecAAC     CodecID = 86018
	CodecOpus    CodecID = 86076
	CodecPCMULaw CodecID = 65542
	CodecPCMALaw CodecID = 65543
	CodecG722    CodecID = 86019
	CodecG729    CodecID = 86069
	CodecUnknown CodecID = 0
)

type FrameType uint8

const (
	FrameTypeVideo    FrameType = 0
	FrameTypeAudio    FrameType = 1
	FrameTypeSubtitle FrameType = 2
	FrameTypeControl  FrameType = 3
)

type FrameFlag uint8

const (
	FlagKeyframe FrameFlag = 0x01
	FlagConfig   FrameFlag = 0x02
	FlagEOF      FrameFlag = 0x04
)

var codecNames = map[CodecID]string{
	CodecH264:    "H264",
	CodecH265:    "H265",
	CodecVP8:     "VP8",
	CodecVP9:     "VP9",
	CodecAV1:     "AV1",
	CodecAAC:     "AAC",
	CodecOpus:    "Opus",
	CodecPCMULaw: "PCMU",
	CodecPCMALaw: "PCMA",
	CodecG722:    "G722",
	CodecG729:    "G729",
}

var codecFFmpegNames = map[CodecID]string{
	CodecH264:    "h264",
	CodecH265:    "hevc",
	CodecVP8:     "vp8",
	CodecVP9:     "vp9",
	CodecAV1:     "av1",
	CodecAAC:     "aac",
	CodecOpus:    "opus",
	CodecPCMULaw: "pcm_mulaw",
	CodecPCMALaw: "pcm_alaw",
	CodecG722:    "g722",
	CodecG729:    "g729",
}

var codecFFmpegFormats = map[CodecID]string{
	CodecH264: "h264",
	CodecH265: "hevc",
	CodecVP8:  "vp8",
	CodecVP9:  "vp9",
	CodecAV1:  "av1",
	CodecAAC:  "aac",
	CodecOpus: "opus",
}

var codecBSF = map[CodecID]string{
	CodecH264: "h264_mp4toannexb",
	CodecH265: "hevc_mp4toannexb",
}

func (c CodecID) String() string {
	if name, ok := codecNames[c]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", c)
}

func (c CodecID) FFmpegName() string {
	if name, ok := codecFFmpegNames[c]; ok {
		return name
	}
	return ""
}

func (c CodecID) FFmpegFormat() string {
	if fmt, ok := codecFFmpegFormats[c]; ok {
		return fmt
	}
	return ""
}

func (c CodecID) BSF() string {
	if bsf, ok := codecBSF[c]; ok {
		return bsf
	}
	return ""
}

func (c CodecID) IsVideo() bool {
	return c >= 0 && c < 0x8000
}

func (c CodecID) IsAudio() bool {
	return c >= 0x8000
}

func CodecIDFromFFmpegName(name string) CodecID {
	switch name {
	case "h264":
		return CodecH264
	case "hevc", "h265":
		return CodecH265
	case "vp8":
		return CodecVP8
	case "vp9":
		return CodecVP9
	case "av1":
		return CodecAV1
	case "aac":
		return CodecAAC
	case "opus":
		return CodecOpus
	case "pcm_mulaw":
		return CodecPCMULaw
	case "pcm_alaw":
		return CodecPCMALaw
	case "g722":
		return CodecG722
	case "g729":
		return CodecG729
	default:
		return CodecUnknown
	}
}

// Not used in current architecture: NAL parsing would be needed for in-process stream analysis or keyframe detection.
func ParseNALType(data []byte, codec CodecID) (naluType int, isKey bool) {
	if len(data) < 5 {
		return 0, false
	}

	switch codec {
	case CodecH264:
		naluType = int(data[4] & 0x1F)
		isKey = (naluType == 5)
		return
	case CodecH265:
		naluType = int((data[4] >> 1) & 0x3F)
		isKey = (naluType == 19 || naluType == 20)
		return
	default:
		return 0, false
	}
}

func IsVideoCodec(codec CodecID) bool {
	return codec.IsVideo()
}

func IsAudioCodec(codec CodecID) bool {
	return codec.IsAudio()
}

func (f FrameType) String() string {
	switch f {
	case FrameTypeVideo:
		return "video"
	case FrameTypeAudio:
		return "audio"
	case FrameTypeSubtitle:
		return "subtitle"
	case FrameTypeControl:
		return "control"
	default:
		return "unknown"
	}
}
