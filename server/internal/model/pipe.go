package model

import (
	"time"
)

// Pipe 管道模型
type Pipe struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// uniqueIndex 兜底查重 TOCTOU：同一输入输出对只允许一条管道
	InputID    string    `json:"input_id" gorm:"type:varchar(36);not null;uniqueIndex:uq_pipe_input_output"`
	OutputID   string    `json:"output_id" gorm:"type:varchar(36);not null;uniqueIndex:uq_pipe_input_output"`
	ChannelMap string    `json:"channel_map" gorm:"type:text"`
	MuxSync    bool      `json:"mux_sync" gorm:"default:false"`
	Status     string    `json:"status" gorm:"type:varchar(20);default:stopped"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ChannelMapping 通道映射
type ChannelMapping struct {
	InputChannelID  uint8 `json:"input_channel_id"`
	OutputChannelID uint8 `json:"output_channel_id"`
}

// PipeStatus 管道状态常量
const (
	PipeStatusStopped = "stopped"
	PipeStatusRunning = "running"
	PipeStatusError   = "error"
)
