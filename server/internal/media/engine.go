package media

import "context"

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
	// UDPReadBufferSize UDP 拉流读缓冲大小（字节）；0 用 OS 默认值。
	// 本机回环或高突发场景下调大可减少丢包。
	UDPReadBufferSize int
	// TimestampGrid 时间戳网格化重打开关（按 track）。
	TimestampGrid *TrackGridConfig
}

// TrackGridConfig 每个 track 的网格化重打开关（Video/Audio 独立）。
type TrackGridConfig struct {
	Video bool
	Audio bool
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

// Engine 媒体引擎接口：服务层依赖的媒体能力子集。
// MediaHub 为其实现；测试可用 mock 替代。
type Engine interface {
	Start(ctx context.Context) error
	Stop() error
	CreateInput(config *InputConfig) (Source, error)
	CreateOutput(config *OutputConfig) (Sink, error)
	Connect(inputID, outputID string) error
	Disconnect(inputID, outputID string) error
	RemoveInput(id string) error
	RemoveOutput(id string) error
	StartInput(id string) error
	StartOutput(id string) error
	StartOutputWithFile(id string, filePath string) error
	StartPipe(inputID, outputID string) error
	GetOutput(id string) (Sink, error)
	// GetOutputClients 返回指定输出端当前连接的客户端信息。
	// 输出端未实现 ClientInfoProvider 时返回空列表。
	GetOutputClients(id string) ([]ClientInfo, error)
}

// Compile-time check: MediaHub 实现 Engine 接口。
var _ Engine = (*MediaHub)(nil)
