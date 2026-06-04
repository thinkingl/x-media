package model

import (
	"time"
)

// Input 输入端模型
type Input struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name      string    `json:"name" gorm:"type:varchar(100);not null"`
	Type      string    `json:"type" gorm:"type:varchar(20);not null"`
	Config    string    `json:"config" gorm:"type:text;not null"`
	MediaInfo string    `json:"media_info,omitempty" gorm:"type:text"`
	Status    string    `json:"status" gorm:"type:varchar(20);default:stopped"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InputConfig 输入端配置
type InputConfig struct {
	// MP4文件配置
	Path  string  `json:"path,omitempty"`
	Loop  *bool   `json:"loop,omitempty"`
	Speed float64 `json:"speed,omitempty"`

	// RTSP配置
	URL             string `json:"url,omitempty"`
	Transport       string `json:"transport,omitempty"`
	TimeoutMs       int    `json:"timeout_ms,omitempty"`
	Reconnect       *bool  `json:"reconnect,omitempty"`
	ReconnectIntervalMs int  `json:"reconnect_interval_ms,omitempty"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`

	// 通用配置
	AudioEnable *bool `json:"audio_enable,omitempty"`
	VideoEnable *bool `json:"video_enable,omitempty"`
}

// InputStatus 输入端状态常量
const (
	InputStatusStopped = "stopped"
	InputStatusRunning = "running"
	InputStatusError   = "error"
)

// InputType 输入端类型常量
const (
	InputTypeFile = "file"
	InputTypeRTSP = "rtsp"
	InputTypeRTMP = "rtmp"
	InputTypeHLS  = "hls"
)
