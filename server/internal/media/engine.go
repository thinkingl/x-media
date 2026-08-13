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
}

// Compile-time check: MediaHub 实现 Engine 接口。
var _ Engine = (*MediaHub)(nil)
