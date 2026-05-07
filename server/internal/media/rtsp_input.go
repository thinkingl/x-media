package media

import (
	"context"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// RTSPInput RTSP输入流
type RTSPInput struct {
	mu      sync.RWMutex
	id      string
	config  *InputConfig
	status  StreamStatus
	handler PacketHandler
	cancel  context.CancelFunc
}

// NewRTSPInput 创建RTSP输入流
func NewRTSPInput(config *InputConfig) (*RTSPInput, error) {
	if config.URL == "" {
		return nil, ErrInvalidConfig
	}

	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &RTSPInput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

// ID 获取流ID
func (r *RTSPInput) ID() string {
	return r.id
}

// Start 启动流
func (r *RTSPInput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusRunning {
		return nil
	}

	// TODO: 实际连接RTSP服务器
	// 这里简化处理，实际应该使用LAL库

	r.status = StreamStatusRunning

	// 启动读取协程
	ctx, r.cancel = context.WithCancel(ctx)
	go r.readLoop(ctx)

	logger.Infof("RTSP输入流已启动: %s, URL: %s", r.id, r.config.URL)
	return nil
}

// Stop 停止流
func (r *RTSPInput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusStopped {
		return nil
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.status = StreamStatusStopped
	logger.Infof("RTSP输入流已停止: %s", r.id)
	return nil
}

// Status 获取状态
func (r *RTSPInput) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// ReadPacket 读取数据包
func (r *RTSPInput) ReadPacket() (*MediaPacket, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.status != StreamStatusRunning {
		return nil, ErrStreamNotRunning
	}

	// TODO: 实际从RTSP读取数据
	// 这里返回模拟数据
	return &MediaPacket{
		StreamID:  r.id,
		Timestamp: time.Now().UnixMilli(),
		IsVideo:   true,
		Data:      []byte("rtsp-data"),
	}, nil
}

// OnPacket 设置数据包回调
func (r *RTSPInput) OnPacket(handler PacketHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

// readLoop 读取循环
func (r *RTSPInput) readLoop(ctx context.Context) {
	ticker := time.NewTicker(33 * time.Millisecond) // ~30fps
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pkt, err := r.ReadPacket()
			if err != nil {
				logger.Errorf("读取RTSP数据包失败: %v", err)
				// 尝试重连
				time.Sleep(5 * time.Second)
				continue
			}

			r.mu.RLock()
			handler := r.handler
			r.mu.RUnlock()

			if handler != nil {
				handler(pkt)
			}
		}
	}
}
