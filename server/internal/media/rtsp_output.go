package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// RTSPOutput RTSP输出流
type RTSPOutput struct {
	mu     sync.RWMutex
	id     string
	config *OutputConfig
	status StreamStatus
	cancel context.CancelFunc
}

// NewRTSPOutput 创建RTSP输出流
func NewRTSPOutput(config *OutputConfig) (*RTSPOutput, error) {
	if config.Mode == "" {
		return nil, ErrInvalidConfig
	}

	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &RTSPOutput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

// ID 获取流ID
func (r *RTSPOutput) ID() string {
	return r.id
}

// Start 启动流
func (r *RTSPOutput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusRunning {
		return nil
	}

	// TODO: 实际启动RTSP服务或连接
	// 这里简化处理，实际应该使用LAL库

	r.status = StreamStatusRunning
	_, r.cancel = context.WithCancel(ctx)

	logger.Infof("RTSP输出流已启动: %s, 模式: %s", r.id, r.config.Mode)
	return nil
}

// Stop 停止流
func (r *RTSPOutput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusStopped {
		return nil
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.status = StreamStatusStopped
	logger.Infof("RTSP输出流已停止: %s", r.id)
	return nil
}

// Status 获取状态
func (r *RTSPOutput) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// WritePacket 写入数据包
func (r *RTSPOutput) WritePacket(pkt *MediaPacket) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.status != StreamStatusRunning {
		return ErrStreamNotRunning
	}

	// TODO: 实际写入RTSP数据
	logger.Debugf("写入RTSP数据包: %s, 大小: %d", r.id, len(pkt.Data))
	return nil
}
