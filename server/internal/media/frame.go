package media

import (
	"encoding/binary"
	"fmt"
	"io"
)

var FrameMagic = [2]byte{0x58, 0x4D}

const FrameVersion uint8 = 0x01

type FrameHeader struct {
	Magic      [2]byte
	Version    uint8
	ChannelID  uint8
	FrameType  FrameType
	Codec      CodecID
	Flags      FrameFlag
	PTS        int64
	DTS        int64
	PayloadLen uint32
}

const FrameHeaderSize = 32

func (h *FrameHeader) Encode() []byte {
	buf := make([]byte, FrameHeaderSize)
	buf[0] = h.Magic[0]
	buf[1] = h.Magic[1]
	buf[2] = h.Version
	buf[3] = h.ChannelID
	buf[4] = uint8(h.FrameType)
	binary.BigEndian.PutUint32(buf[5:9], uint32(h.Codec)) // 4 bytes for FFmpeg AVCodecID (e.g. AAC=86018)
	buf[9] = uint8(h.Flags)
	binary.BigEndian.PutUint64(buf[10:18], uint64(h.PTS))
	binary.BigEndian.PutUint64(buf[18:26], uint64(h.DTS))
	binary.BigEndian.PutUint32(buf[26:30], h.PayloadLen)
	return buf
}

func DecodeFrameHeader(data []byte) (*FrameHeader, error) {
	if len(data) < FrameHeaderSize {
		return nil, fmt.Errorf("frame header too short: %d < %d", len(data), FrameHeaderSize)
	}

	h := &FrameHeader{
		Magic:      [2]byte{data[0], data[1]},
		Version:    data[2],
		ChannelID:  data[3],
		FrameType:  FrameType(data[4]),
		Codec:      CodecID(binary.BigEndian.Uint32(data[5:9])),
		Flags:      FrameFlag(data[9]),
		PTS:        int64(binary.BigEndian.Uint64(data[10:18])),
		DTS:        int64(binary.BigEndian.Uint64(data[18:26])),
		PayloadLen: binary.BigEndian.Uint32(data[26:30]),
	}

	if h.Magic != FrameMagic {
		return nil, fmt.Errorf("invalid frame magic: %v", h.Magic)
	}

	if h.Version != FrameVersion {
		return nil, fmt.Errorf("unsupported frame version: %d", h.Version)
	}

	return h, nil
}

type Frame struct {
	Header  FrameHeader
	Payload []byte
}

func NewFrame(channelID uint8, frameType FrameType, codec CodecID, flags FrameFlag, pts, dts int64, payload []byte) *Frame {
	return &Frame{
		Header: FrameHeader{
			Magic:      FrameMagic,
			Version:    FrameVersion,
			ChannelID:  channelID,
			FrameType:  frameType,
			Codec:      codec,
			Flags:      flags,
			PTS:        pts,
			DTS:        dts,
			PayloadLen: uint32(len(payload)),
		},
		Payload: payload,
	}
}

func (f *Frame) Encode() []byte {
	buf := make([]byte, FrameHeaderSize+len(f.Payload))
	copy(buf[:FrameHeaderSize], f.Header.Encode())
	copy(buf[FrameHeaderSize:], f.Payload)
	return buf
}

func ReadFrame(r io.Reader) (*Frame, error) {
	headerBuf := make([]byte, FrameHeaderSize)
	_, err := io.ReadFull(r, headerBuf)
	if err != nil {
		return nil, err
	}

	h, err := DecodeFrameHeader(headerBuf)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, h.PayloadLen)
	if h.PayloadLen > 0 {
		_, err = io.ReadFull(r, payload)
		if err != nil {
			return nil, err
		}
	}

	return &Frame{Header: *h, Payload: payload}, nil
}

func WriteFrame(w io.Writer, f *Frame) error {
	_, err := w.Write(f.Encode())
	return err
}

func (f *Frame) ToMediaPacket() *MediaPacket {
	return &MediaPacket{
		StreamID:   fmt.Sprintf("ch%d", f.Header.ChannelID),
		ChannelID:  f.Header.ChannelID,
		Kind:       f.Header.FrameType.String(),
		CodecType:  f.Header.Codec.String(),
		CodecID:    f.Header.Codec,
		IsVideo:    f.Header.FrameType == FrameTypeVideo,
		IsAudio:    f.Header.FrameType == FrameTypeAudio,
		IsKeyFrame: f.Header.Flags&FlagKeyframe != 0,
		Data:       f.Payload,
		PTS:        f.Header.PTS,
		DTS:        f.Header.DTS,
		Timestamp:  f.Header.PTS / 1000,
	}
}

func MediaPacketToFrame(pkt *MediaPacket) *Frame {
	var flags FrameFlag
	if pkt.IsKeyFrame {
		flags |= FlagKeyframe
	}

	return NewFrame(
		pkt.ChannelID,
		FrameTypeFromString(pkt.Kind),
		pkt.CodecID,
		flags,
		pkt.PTS,
		pkt.DTS,
		pkt.Data,
	)
}

func FrameTypeFromString(s string) FrameType {
	switch s {
	case "video":
		return FrameTypeVideo
	case "audio":
		return FrameTypeAudio
	case "subtitle":
		return FrameTypeSubtitle
	case "control":
		return FrameTypeControl
	default:
		return FrameTypeVideo
	}
}
