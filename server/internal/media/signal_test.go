package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalEncodeDecodeRoundTrip(t *testing.T) {
	req := &SubscribeRequest{Channels: []uint8{0, 1}}
	sig, err := NewSignal(SignalSubscribe, 42, req)
	require.NoError(t, err)

	data, err := sig.Encode()
	require.NoError(t, err)

	dec, err := DecodeSignal(data)
	require.NoError(t, err)
	assert.Equal(t, SignalSubscribe, dec.Type)
	assert.Equal(t, uint64(42), dec.RequestID)
	assert.False(t, dec.IsReply)

	var got SubscribeRequest
	require.NoError(t, dec.DecodePayload(&got))
	assert.Equal(t, req.Channels, got.Channels)
}

func TestSignalEmptyType(t *testing.T) {
	_, err := DecodeSignal([]byte(`{"type":0}`))
	assert.Error(t, err)
}

func TestSignalInvalidJSON(t *testing.T) {
	_, err := DecodeSignal([]byte(`{`))
	assert.Error(t, err)
}

func TestNewReply(t *testing.T) {
	req, err := NewSignal(SignalSeek, 7, &SeekRequest{ChannelID: 0, PTS: 12345})
	require.NoError(t, err)

	reply, err := NewReply(req, &SubscribeResponse{})
	require.NoError(t, err)
	assert.Equal(t, SignalSeek, reply.Type)
	assert.Equal(t, uint64(7), reply.RequestID)
	assert.True(t, reply.IsReply)
}

func TestSignalTypeString(t *testing.T) {
	assert.Equal(t, "subscribe", SignalSubscribe.String())
	assert.Equal(t, "error", SignalError.String())
	assert.Equal(t, "unknown", SignalType(99).String())
}

func TestSignalNilPayload(t *testing.T) {
	sig, err := NewSignal(SignalStart, 1, nil)
	require.NoError(t, err)
	assert.Empty(t, sig.Payload)

	// 无 payload 解码到任意结构应为空操作
	var v SubscribeResponse
	require.NoError(t, sig.DecodePayload(&v))
}
