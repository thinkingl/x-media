package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// HTTPFLVOutput HTTP-FLV输出流
type HTTPFLVOutput struct {
	mu     sync.RWMutex
	id     string
	config *OutputConfig
	status StreamStatus
	cancel context.CancelFunc
}

// NewHTTPFLVOutput 创建HTTP-FLV输出流
func NewHTTPFLVOutput(config *OutputConfig) (*HTTPFLVOutput, error) {
	if config.Addr == "" {
		return nil, ErrInvalidConfig
	}

	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &HTTPFLVOutput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

// ID 获取流ID
func (h *HTTPFLVOutput) ID() string {
	return h.id
}

// Start 启动流
func (h *HTTPFLVOutput) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.status == StreamStatusRunning {
		return nil
	}

	// TODO: 实际启动HTTP服务
	// 这里简化处理，实际应该使用LAL库

	h.status = StreamStatusRunning
	_, h.cancel = context.WithCancel(ctx)

	logger.Infof("HTTP-FLV输出流已启动: %s, 地址: %s", h.id, h.config.Addr)
	return nil
}

// Stop 停止流
func (h *HTTPFLVOutput) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.status == StreamStatusStopped {
		return nil
	}

	if h.cancel != nil {
		h.cancel()
	}

	h.status = StreamStatusStopped
	logger.Infof("HTTP-FLV输出流已停止: %s", h.id)
	return nil
}

// Status 获取状态
func (h *HTTPFLVOutput) Status() StreamStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

// WritePacket 写入数据包
func (h *HTTPFLVOutput) WritePacket(pkt *MediaPacket) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.status != StreamStatusRunning {
		return ErrStreamNotRunning
	}

	// TODO: 实际写入HTTP-FLV数据
	logger.Debugf("写入HTTP-FLV数据包: %s, 大小: %d", h.id, len(pkt.Data))
	return nil
}
