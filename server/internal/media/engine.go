package media

import (
	"context"
)

// MediaEngine 媒体引擎接口
type MediaEngine interface {
	// Start 启动引擎
	Start(ctx context.Context) error
	// Stop 停止引擎
	Stop() error
	// CreateInput 创建输入流
	CreateInput(config *InputConfig) (InputStream, error)
	// CreateOutput 创建输出流
	CreateOutput(config *OutputConfig) (OutputStream, error)
	// Connect 连接输入输出流
	Connect(inputID, outputID string) error
	// Disconnect 断开连接
	Disconnect(inputID, outputID string) error
	// RemoveInput 移除输入流
	RemoveInput(id string) error
	// RemoveOutput 移除输出流
	RemoveOutput(id string) error
}

// InputStream 输入流接口
type InputStream interface {
	ID() string
	Start(ctx context.Context) error
	Stop() error
	Status() StreamStatus
	ReadPacket() (*MediaPacket, error)
	OnPacket(handler PacketHandler)
}

// OutputStream 输出流接口
type OutputStream interface {
	ID() string
	Start(ctx context.Context) error
	Stop() error
	Status() StreamStatus
	WritePacket(pkt *MediaPacket) error
}

// StreamStatus 流状态
type StreamStatus string

const (
	StreamStatusStopped StreamStatus = "stopped"
	StreamStatusRunning StreamStatus = "running"
	StreamStatusError   StreamStatus = "error"
)

// InputConfig 输入流配置
type InputConfig struct {
	ID        string
	Type      string
	Path      string // 文件路径
	URL       string // 网络流URL
	Loop      bool
	Speed     float64
	Transport string // tcp/udp
	Timeout   int
}

// OutputConfig 输出流配置
type OutputConfig struct {
	ID        string
	Type      string
	URL       string
	Addr      string
	Mode      string // push/server
	Transport string
}

// MediaPacket 媒体数据包
type MediaPacket struct {
	StreamID   string
	ChannelID  uint8
	Kind       string
	CodecID    CodecID
	Timestamp  int64
	IsVideo    bool
	IsAudio    bool
	IsKeyFrame bool
	Data       []byte
	PTS        int64
	DTS        int64
	CodecType  string
	CodecConfig []byte
}

// PacketHandler 数据包处理函数
type PacketHandler func(pkt *MediaPacket)

// MediaStats 媒体统计信息
type MediaStats struct {
	BytesIn    int64
	BytesOut   int64
	Bitrate    int64
	FPS        float64
	PacketLoss float64
}
