package media

import "context"

// Source 媒体源适配层接口。
//
// 将具体媒体源（MP4/RTSP 等）转换为标准帧，并响应 sink 的控制信令。
// 一个 Source 可被多个 Sink 订阅（fan-out），由实现方管理。
type Source interface {
	// ID 返回媒体源唯一标识。
	ID() string
	// Start 启动媒体源（内部开始 demux/拉流）。
	Start(ctx context.Context) error
	// Stop 停止媒体源。
	Stop() error
	// Status 返回当前状态。
	Status() StreamStatus
	// Streams 返回当前全部子通道媒体信息。
	Streams() ([]StreamInfo, error)
	// Subscribe 处理 sink 的订阅请求，返回订阅对应的媒体信息。
	Subscribe(ctx context.Context, req *SubscribeRequest) (*SubscribeResponse, error)
	// Unsubscribe 处理 sink 退订。
	Unsubscribe(ctx context.Context, channels []uint8) error
	// Signal 处理其他控制信令（Start/Pause/Resume/Stop/Seek/GetStreamInfo）。
	Signal(ctx context.Context, sig *Signal) (*Signal, error)
	// SetFrameHandler 注册帧回调（Pipe 会注册自己作为转发入口）。
	SetFrameHandler(h FrameHandler)
	// AddFrameHandler 追加帧回调，支持多 sink 订阅同一 source（fan-out）。
	AddFrameHandler(h FrameHandler)
}

// FrameHandler 帧回调。数据面：Source → Pipe → Sink，单向高吞吐。
type FrameHandler func(f *Frame)

// Sink 媒体汇适配层接口。
//
// 将标准帧封装为目标协议（RTMP/RTSP/HTTP-FLV）。
type Sink interface {
	// ID 返回媒体汇唯一标识。
	ID() string
	// Start 启动媒体汇。
	Start(ctx context.Context) error
	// Stop 停止媒体汇。
	Stop() error
	// Status 返回当前状态。
	Status() StreamStatus
	// WriteFrame 接收一个标准帧（数据面）。
	WriteFrame(f *Frame) error
	// Configure 在管道启动时由 Pipe 调用，提供 source 的媒体信息用于协议协商。
	Configure(streams []StreamInfo) error
	// Notify 接收 source 的异步事件（InfoUpdate/StateChange/Error）。
	Notify(sig *Signal) error
}

// Pipe 媒体管道。source 与 sink 之间的标准契约。
//
// 数据面：Source → Pipe → Sink（单向、高吞吐、带背压）。
// 控制面：sink 通过 Pipe 向 source 发信令（双向）。
//
// 一个 Pipe 绑定一个 source 与一个 sink；fan-out（一对多）由 Source 端管理
// （多个 sink 各自经独立 Pipe 订阅同一 source）。
type Pipe interface {
	// Bind 绑定 source 与 sink。
	Bind(source Source, sink Sink) error
	// Start 启动管道数据面与控制面。
	Start(ctx context.Context) error
	// Stop 停止管道。
	Stop() error
	// SendSignal sink→source 发控制信令，同步返回响应。
	SendSignal(ctx context.Context, sig *Signal) (*Signal, error)
}
