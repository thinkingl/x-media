package media

import (
	"encoding/json"
	"fmt"
	"time"
)

type StreamInfo struct {
	ChannelID   uint8          `json:"channel_id"`
	Kind        string         `json:"kind"`
	CodecID     CodecID        `json:"codec_id"`
	CodecName   string         `json:"codec_name"`
	ClockRate   int            `json:"clock_rate"`
	Parameters  map[string]any `json:"parameters"`
	CodecConfig []byte         `json:"codec_config"`
}

type SessionOffer struct {
	Type      string       `json:"type"`
	SessionID string       `json:"session_id"`
	Timestamp int64        `json:"timestamp"`
	Streams   []StreamInfo `json:"streams"`
}

type SessionAnswer struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id"`
	AcceptedChannels []uint8 `json:"accepted_channels"`
	RejectedChannels []uint8 `json:"rejected_channels"`
}

type SessionUpdate struct {
	Type      string       `json:"type"`
	SessionID string       `json:"session_id"`
	Timestamp int64        `json:"timestamp"`
	Streams   []StreamInfo `json:"streams"`
}

func NewSessionOffer(sessionID string, streams []StreamInfo) *SessionOffer {
	return &SessionOffer{
		Type:      "offer",
		SessionID: sessionID,
		Timestamp: time.Now().UnixMilli(),
		Streams:   streams,
	}
}

func (o *SessionOffer) Encode() ([]byte, error) {
	return json.Marshal(o)
}

func DecodeSessionOffer(data []byte) (*SessionOffer, error) {
	var o SessionOffer
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

func (a *SessionAnswer) Encode() ([]byte, error) {
	return json.Marshal(a)
}

func DecodeSessionAnswer(data []byte) (*SessionAnswer, error) {
	var a SessionAnswer
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (u *SessionUpdate) Encode() ([]byte, error) {
	return json.Marshal(u)
}

func DecodeSessionUpdate(data []byte) (*SessionUpdate, error) {
	var u SessionUpdate
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func DetectStreamsFromFFprobe(probeData []byte) ([]StreamInfo, error) {
	var probe struct {
		Streams []struct {
			Index        int    `json:"index"`
			CodecName    string `json:"codec_name"`
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			RFrameRate   string `json:"r_frame_rate"`
			SampleRate   string `json:"sample_rate"`
			Channels     int    `json:"channels"`
			ChannelLayout string `json:"channel_layout"`
			BitRate      string `json:"bit_rate"`
			Profile      string `json:"profile"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
	}

	if err := json.Unmarshal(probeData, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	var streams []StreamInfo
	channelID := uint8(0)

	for _, s := range probe.Streams {
		codecID := CodecIDFromFFmpegName(s.CodecName)
		if codecID == CodecUnknown {
			continue
		}

		stream := StreamInfo{
			ChannelID: channelID,
			CodecID:   codecID,
			CodecName: codecID.String(),
			Parameters: make(map[string]any),
		}

		if s.CodecType == "video" {
			stream.Kind = "video"
			stream.ClockRate = 90000
			stream.Parameters["width"] = s.Width
			stream.Parameters["height"] = s.Height
			stream.Parameters["profile"] = s.Profile
			if fps, err := parseFrameRate(s.RFrameRate); err == nil {
				stream.Parameters["fps"] = fps
			}
		} else if s.CodecType == "audio" {
			stream.Kind = "audio"
			stream.Parameters["sample_rate"] = s.SampleRate
			stream.Parameters["channels"] = s.Channels
			stream.Parameters["channel_layout"] = s.ChannelLayout
			stream.Parameters["profile"] = s.Profile
			if sr, err := parseInt(s.SampleRate); err == nil {
				stream.ClockRate = sr
			}
		} else {
			continue
		}

		streams = append(streams, stream)
		channelID++
	}

	return streams, nil
}

func parseFrameRate(s string) (float64, error) {
	var num, den int
	if _, err := fmt.Sscanf(s, "%d/%d", &num, &den); err != nil {
		return 0, err
	}
	if den == 0 {
		return 0, fmt.Errorf("zero denominator")
	}
	return float64(num) / float64(den), nil
}

func parseInt(s string) (int, error) {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, err
	}
	return v, nil
}
