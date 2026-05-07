package media

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// FileInput 文件输入流
type FileInput struct {
	mu      sync.RWMutex
	id      string
	config  *InputConfig
	file    *os.File
	status  StreamStatus
	handler PacketHandler
	cancel  context.CancelFunc
}

// NewFileInput 创建文件输入流
func NewFileInput(config *InputConfig) (*FileInput, error) {
	if config.Path == "" {
		return nil, ErrInvalidConfig
	}

	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &FileInput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

// ID 获取流ID
func (f *FileInput) ID() string {
	return f.id
}

// Start 启动流
func (f *FileInput) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusRunning {
		return nil
	}

	// 打开文件
	file, err := os.Open(f.config.Path)
	if err != nil {
		logger.Errorf("打开文件失败 %s: %v", f.config.Path, err)
		f.status = StreamStatusError
		return err
	}

	f.file = file
	f.status = StreamStatusRunning

	// 启动读取协程
	ctx, f.cancel = context.WithCancel(ctx)
	go f.readLoop(ctx)

	logger.Infof("文件输入流已启动: %s, 文件: %s", f.id, f.config.Path)
	return nil
}

// Stop 停止流
func (f *FileInput) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusStopped {
		return nil
	}

	if f.cancel != nil {
		f.cancel()
	}

	if f.file != nil {
		f.file.Close()
		f.file = nil
	}

	f.status = StreamStatusStopped
	logger.Infof("文件输入流已停止: %s", f.id)
	return nil
}

// Status 获取状态
func (f *FileInput) Status() StreamStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.status
}

// ReadPacket 读取数据包
func (f *FileInput) ReadPacket() (*MediaPacket, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.status != StreamStatusRunning {
		return nil, ErrStreamNotRunning
	}

	// 读取文件数据
	buf := make([]byte, 4096)
	n, err := f.file.Read(buf)
	if err != nil {
		return nil, err
	}

	return &MediaPacket{
		StreamID:  f.id,
		Timestamp: time.Now().UnixMilli(),
		IsVideo:   true,
		Data:      buf[:n],
	}, nil
}

// OnPacket 设置数据包回调
func (f *FileInput) OnPacket(handler PacketHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}

// readLoop 读取循环
func (f *FileInput) readLoop(ctx context.Context) {
	ticker := time.NewTicker(33 * time.Millisecond) // ~30fps
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pkt, err := f.ReadPacket()
			if err != nil {
				if f.config.Loop {
					// 循环播放，重置文件位置
					f.mu.Lock()
					if f.file != nil {
						f.file.Seek(0, 0)
					}
					f.mu.Unlock()
					continue
				}
				logger.Errorf("读取数据包失败: %v", err)
				return
			}

			f.mu.RLock()
			handler := f.handler
			f.mu.RUnlock()

			if handler != nil {
				handler(pkt)
			}
		}
	}
}
