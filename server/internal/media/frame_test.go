package media

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameHeaderRoundTrip(t *testing.T) {
	h := &FrameHeader{
		Magic:      FrameMagic,
		Version:    FrameVersion,
		ChannelID:  1,
		FrameType:  FrameTypeVideo,
		Codec:      CodecH264,
		Flags:      FlagKeyframe,
		PTS:        12345678901234,
		DTS:        12345678901000,
		PayloadLen: 1024,
	}

	buf := h.Encode()
	assert.Len(t, buf, FrameHeaderSize)

	dec, err := DecodeFrameHeader(buf)
	require.NoError(t, err)
	assert.Equal(t, h.Magic, dec.Magic)
	assert.Equal(t, h.Version, dec.Version)
	assert.Equal(t, h.ChannelID, dec.ChannelID)
	assert.Equal(t, h.FrameType, dec.FrameType)
	assert.Equal(t, h.Codec, dec.Codec)
	assert.Equal(t, h.Flags, dec.Flags)
	assert.Equal(t, h.PTS, dec.PTS)
	assert.Equal(t, h.DTS, dec.DTS)
	assert.Equal(t, h.PayloadLen, dec.PayloadLen)
}

func TestFrameHeaderInvalidMagic(t *testing.T) {
	buf := make([]byte, FrameHeaderSize)
	buf[0] = 0x00
	buf[1] = 0x41 // wrong magic
	buf[2] = FrameVersion

	_, err := DecodeFrameHeader(buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "magic")
}

func TestFrameHeaderUnsupportedVersion(t *testing.T) {
	buf := make([]byte, FrameHeaderSize)
	copy(buf[:2], FrameMagic[:])
	buf[2] = 0xFF

	_, err := DecodeFrameHeader(buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestFrameHeaderTooShort(t *testing.T) {
	_, err := DecodeFrameHeader(make([]byte, FrameHeaderSize-1))
	assert.Error(t, err)
}

func TestFrameEncodeDecode(t *testing.T) {
	f := NewFrame(2, FrameTypeAudio, CodecAAC, FlagConfig, 1000, 1000, []byte{0x11, 0x22, 0x33})
	buf := f.Encode()
	assert.Len(t, buf, FrameHeaderSize+3)

	var br bytes.Buffer
	require.NoError(t, WriteFrame(&br, f))

	rd, err := ReadFrame(&br)
	require.NoError(t, err)
	assert.Equal(t, f.Header.ChannelID, rd.Header.ChannelID)
	assert.Equal(t, f.Header.FrameType, rd.Header.FrameType)
	assert.Equal(t, f.Header.Codec, rd.Header.Codec)
	assert.Equal(t, f.Header.Flags, rd.Header.Flags)
	assert.Equal(t, f.Header.PTS, rd.Header.PTS)
	assert.Equal(t, f.Header.DTS, rd.Header.DTS)
	assert.Equal(t, f.Payload, rd.Payload)
}

func TestFrameHeaderLayout(t *testing.T) {
	// 验证字段在 30 字节头中的精确偏移，保证协议线格式稳定。
	h := &FrameHeader{
		Magic:      FrameMagic,
		Version:    FrameVersion,
		ChannelID:  7,
		FrameType:  FrameTypeVideo,
		Codec:      CodecH264,
		Flags:      FlagKeyframe,
		PTS:        0x1122334455667788,
		DTS:        0x99AABBCCDDEEFF,
		PayloadLen: 0xCAFEBABE,
	}
	buf := h.Encode()

	assert.Equal(t, byte(0x58), buf[0])                     // Magic[0]
	assert.Equal(t, byte(0x4D), buf[1])                     // Magic[1]
	assert.Equal(t, byte(0x02), buf[2])                     // Version
	assert.Equal(t, byte(7), buf[3])                        // ChannelID
	assert.Equal(t, byte(FrameTypeVideo), buf[4])           // FrameType
	assert.Equal(t, binary.BigEndian.Uint32(buf[5:9]), uint32(CodecH264))
	assert.Equal(t, byte(FlagKeyframe), buf[9])             // Flags
	assert.Equal(t, uint64(0x1122334455667788), binary.BigEndian.Uint64(buf[10:18]))
	assert.Equal(t, uint64(0x99AABBCCDDEEFF), binary.BigEndian.Uint64(buf[18:26]))
	assert.Equal(t, uint32(0xCAFEBABE), binary.BigEndian.Uint32(buf[26:30]))
}

func TestReadFrameTruncated(t *testing.T) {
	f := NewFrame(0, FrameTypeVideo, CodecH264, 0, 0, 0, []byte{1, 2, 3, 4})
	buf := f.Encode()
	// 截断 payload
	_, err := ReadFrame(bytes.NewReader(buf[:FrameHeaderSize+2]))
	assert.Error(t, err)
}
