package media

// 客户端（拉流者）信息结构，供前端展示与排障。
// RTSPSink 等实现的 Sink 可通过 ClientInfoProvider 接口暴露当前连接的客户端。

import "time"

// ClientInfo 一个拉流客户端的信息。
type ClientInfo struct {
	// Address 客户端 IP:端口
	Address string `json:"address"`
	// UserAgent 客户端 UA（VLC/ffmpeg/浏览器等）
	UserAgent string `json:"user_agent"`
	// Transport 传输协议：tcp/udp
	Transport string `json:"transport"`
	// ConnectedAt 建立连接的时间
	ConnectedAt time.Time `json:"connected_at"`
}

// ClientInfoProvider Sink 可选实现的接口：返回当前连接的客户端信息。
// 各 sink 按自身协议暴露（如 RTSPSink 暴露 RTSP reader）。
type ClientInfoProvider interface {
	// Clients 返回当前连接的客户端列表。
	Clients() []ClientInfo
}
