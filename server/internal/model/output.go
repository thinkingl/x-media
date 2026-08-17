package model

import (
	"time"
)

// Output 输出端模型
type Output struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name      string    `json:"name" gorm:"type:varchar(100);not null"`
	Type      string    `json:"type" gorm:"type:varchar(20);not null"`
	Config    string    `json:"config" gorm:"type:text;not null"`
	Status    string    `json:"status" gorm:"type:varchar(20);default:stopped"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OutputConfig 输出端配置
type OutputConfig struct {
	// RTMP配置
	URL                 string `json:"url,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	ConnectTimeoutMs    int    `json:"connect_timeout_ms,omitempty"`
	WriteTimeoutMs      int    `json:"write_timeout_ms,omitempty"`
	TLSEnable           *bool  `json:"tls_enable,omitempty"`
	TLSCertFile         string `json:"tls_cert_file,omitempty"`
	TLSKeyFile          string `json:"tls_key_file,omitempty"`

	// RTSP配置
	Mode      string `json:"mode,omitempty"` // push 或 server
	Addr      string `json:"addr,omitempty"`
	Transport string `json:"transport,omitempty"`
	AuthEnable *bool  `json:"auth_enable,omitempty"`

	// HTTP-FLV配置
	URLPattern   string `json:"url_pattern,omitempty"`
	EnableHTTPS  *bool  `json:"enable_https,omitempty"`
	HTTPSAddr    string `json:"https_addr,omitempty"`
	GOPNum       int    `json:"gop_num,omitempty"`
	CrossDomain  *bool  `json:"cross_domain,omitempty"`
	CORSOrigin   string `json:"cors_origin,omitempty"`

	// 通用配置
	Reconnect           *bool `json:"reconnect,omitempty"`
	ReconnectIntervalMs int   `json:"reconnect_interval_ms,omitempty"`
	Username            string `json:"username,omitempty"`
	Password            string `json:"password,omitempty"`
}

// OutputStatus 输出端状态常量
const (
	OutputStatusStopped = "stopped"
	OutputStatusRunning = "running"
	OutputStatusError   = "error"
)

// OutputType 输出端类型常量
const (
	OutputTypeRTMP    = "rtmp"
	OutputTypeRTSP    = "rtsp"
	OutputTypeHTTPFLV = "http-flv"
	OutputTypeHLS     = "hls"
	OutputTypeWebRTC  = "webrtc"
)
