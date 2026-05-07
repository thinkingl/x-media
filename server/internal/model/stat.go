package model

import (
	"time"
)

// Stat 统计数据
type Stat struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	EntityType string    `json:"entity_type" gorm:"type:varchar(20);not null"`
	EntityID   string    `json:"entity_id" gorm:"type:varchar(36);not null"`
	Timestamp  time.Time `json:"timestamp" gorm:"autoCreateTime"`
	BytesIn    int64     `json:"bytes_in" gorm:"default:0"`
	BytesOut   int64     `json:"bytes_out" gorm:"default:0"`
	Bitrate    int64     `json:"bitrate" gorm:"default:0"`
	FPS        float64   `json:"fps" gorm:"default:0"`
	Connections int      `json:"connections" gorm:"default:0"`
}

// StatEntityType 统计实体类型常量
const (
	StatEntityTypeInput  = "input"
	StatEntityTypeOutput = "output"
	StatEntityTypePipe   = "pipe"
)

// StatsResponse 统计响应
type StatsResponse struct {
	TotalBytesIn    int64   `json:"total_bytes_in"`
	TotalBytesOut   int64   `json:"total_bytes_out"`
	TotalBitrate    int64   `json:"total_bitrate"`
	ActiveInputs    int     `json:"active_inputs"`
	ActiveOutputs   int     `json:"active_outputs"`
	ActivePipes     int     `json:"active_pipes"`
	AverageFPS      float64 `json:"average_fps"`
}
