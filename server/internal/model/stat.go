package model

// StatsResponse 统计响应
type StatsResponse struct {
	TotalBytesIn  int64   `json:"total_bytes_in"`
	TotalBytesOut int64   `json:"total_bytes_out"`
	TotalBitrate  int64   `json:"total_bitrate"`
	ActiveInputs  int     `json:"active_inputs"`
	ActiveOutputs int     `json:"active_outputs"`
	ActivePipes   int     `json:"active_pipes"`
	AverageFPS    float64 `json:"average_fps"`
}
