package media

import (
	"encoding/json"
	"fmt"
)

// SignalType 信令消息类型（控制面）。
type SignalType uint8

const (
	// SignalSubscribe sink→source：订阅子通道，返回媒体信息。
	SignalSubscribe SignalType = iota + 1
	// SignalUnsubscribe sink→source：断开订阅。
	SignalUnsubscribe
	// SignalStart sink→source：开始发送媒体流。
	SignalStart
	// SignalPause sink→source：暂停发送。
	SignalPause
	// SignalResume sink→source：恢复发送。
	SignalResume
	// SignalStop sink→source：停止发送。
	SignalStop
	// SignalSeek sink→source：跳转到指定 PTS（文件源/录制）。
	SignalSeek
	// SignalGetStreamInfo sink→source：查询媒体信息。
	SignalGetStreamInfo
	// SignalInfoUpdate source→sink：动态增删子通道/参数变化。
	SignalInfoUpdate
	// SignalStateChange source→sink：状态迁移（started/stopped/error）。
	SignalStateChange
	// SignalError source→sink：错误通知。
	SignalError
)

func (t SignalType) String() string {
	switch t {
	case SignalSubscribe:
		return "subscribe"
	case SignalUnsubscribe:
		return "unsubscribe"
	case SignalStart:
		return "start"
	case SignalPause:
		return "pause"
	case SignalResume:
		return "resume"
	case SignalStop:
		return "stop"
	case SignalSeek:
		return "seek"
	case SignalGetStreamInfo:
		return "get_stream_info"
	case SignalInfoUpdate:
		return "info_update"
	case SignalStateChange:
		return "state_change"
	case SignalError:
		return "error"
	default:
		return "unknown"
	}
}

// Signal 信令信封。同一编解码用于进程内与进程间传输。
// 同步请求：Type 为请求类型，RequestID 标识；响应 Type 同请求类型、IsReply=true、RequestID 回填。
// 异步事件：IsReply=false，无 RequestID。
type Signal struct {
	Type      SignalType      `json:"type"`
	RequestID uint64          `json:"request_id,omitempty"`
	IsReply   bool            `json:"is_reply,omitempty"`
	Error     string          `json:"error,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Encode 序列化信令。
func (s *Signal) Encode() ([]byte, error) {
	return json.Marshal(s)
}

// DecodeSignal 反序列化信令。
func DecodeSignal(data []byte) (*Signal, error) {
	var s Signal
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Type == 0 {
		return nil, fmt.Errorf("invalid signal: empty type")
	}
	return &s, nil
}

// ---- 请求/响应 Payload ----

// SubscribeRequest 订阅请求。Channels 为空表示订阅全部子通道。
type SubscribeRequest struct {
	Channels []uint8 `json:"channels,omitempty"`
}

// SubscribeResponse 订阅响应，携带媒体信息。
type SubscribeResponse struct {
	Streams []StreamInfo `json:"streams"`
}

// SeekRequest 跳转请求。PTS 单位为该子通道的流内 timescale。
type SeekRequest struct {
	ChannelID uint8 `json:"channel_id"`
	PTS       int64 `json:"pts"`
}

// InfoUpdateEvent 动态子通道变更事件。
type InfoUpdateEvent struct {
	Streams []StreamInfo `json:"streams"`
}

// StateChangeEvent 状态迁移事件。
type StateChangeEvent struct {
	State string `json:"state"`
}

// ErrorEvent 错误通知事件。
type ErrorEvent struct {
	Message string `json:"message"`
}

// ---- 便捷构造 ----

// NewSignal 构造信封。
func NewSignal(t SignalType, requestID uint64, payload any) (*Signal, error) {
	s := &Signal{Type: t, RequestID: requestID}
	if payload == nil {
		return s, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	s.Payload = b
	return s, nil
}

// NewReply 构造同步响应（Type 为对应请求类型，IsReply=true）。
func NewReply(req *Signal, payload any) (*Signal, error) {
	s := &Signal{Type: req.Type, RequestID: req.RequestID, IsReply: true}
	if payload == nil {
		return s, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	s.Payload = b
	return s, nil
}

// DecodePayload 将信封 payload 解码为具体结构。
func (s *Signal) DecodePayload(v any) error {
	if len(s.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(s.Payload, v)
}
