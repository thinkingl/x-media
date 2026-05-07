package model

import (
	"time"
)

// Pipe 管道模型
type Pipe struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	InputID   string    `json:"input_id" gorm:"type:varchar(36);not null"`
	OutputID  string    `json:"output_id" gorm:"type:varchar(36);not null"`
	Status    string    `json:"status" gorm:"type:varchar(20);default:stopped"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PipeConfig 管道配置
type PipeConfig struct {
	InputID            string `json:"input_id"`
	OutputID           string `json:"output_id"`
	AutoStart          *bool  `json:"auto_start,omitempty"`
	BufferSize         int    `json:"buffer_size,omitempty"`
	RestartOnError     *bool  `json:"restart_on_error,omitempty"`
	MaxRestartCount    int    `json:"max_restart_count,omitempty"`
	RestartIntervalMs  int    `json:"restart_interval_ms,omitempty"`
}

// PipeStatus 管道状态常量
const (
	PipeStatusStopped = "stopped"
	PipeStatusRunning = "running"
	PipeStatusError   = "error"
)
