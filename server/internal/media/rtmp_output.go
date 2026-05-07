package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// RTMPOutput RTMP输出流
type RTMPOutput struct {
	mu     sync.RWMutex
	id     string
	config *OutputConfig
	status StreamStatus
	cancel context.CancelFunc
}

// NewRTMPOutput 创建RTMP输出流
func NewRTMPOutput(config *OutputConfig) (*RTMPOutput, error) {
	if config.URL == "" {
		return nil, ErrInvalidConfig
	}

	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &RTMPOutput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

// ID 获取流ID
func (r *RTMPOutput) ID() string {
	return r.id
}

// Start 启动流
func (r *RTMPOutput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusRunning {
		return nil
	}

	// TODO: 实际连接RTMP服务器
	// 这里简化处理，实际应该使用LAL库

	r.status = StreamStatusRunning
	_, r.cancel = context.WithCancel(ctx)

	logger.Infof("RTMP输出流已启动: %s, URL: %s", r.id, r.config.URL)
	return nil
}

// Stop 停止流
func (r *RTMPOutput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusStopped {
		return nil
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.status = StreamStatusStopped
	logger.Infof("RTMP输出流已停止: %s", r.id)
	return nil
}

// Status 获取状态
func (r *RTMPOutput) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// WritePacket 写入数据包
func (r *RTMPOutput) WritePacket(pkt *MediaPacket) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.status != StreamStatusRunning {
		return ErrStreamNotRunning
	}

	// TODO: 实际写入RTMP数据
	logger.Debugf("写入RTMP数据包: %s, 大小: %d", r.id, len(pkt.Data))
	return nil
}
