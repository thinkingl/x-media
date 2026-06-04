package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type FileInput struct {
	mu      sync.RWMutex
	id      string
	config  *InputConfig
	status  StreamStatus
	handler PacketHandler
	cancel  context.CancelFunc
	streams []StreamInfo
}

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

func (f *FileInput) ID() string           { return f.id }
func (f *FileInput) Status() StreamStatus { f.mu.RLock(); defer f.mu.RUnlock(); return f.status }

func (f *FileInput) GetStreams() []StreamInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.streams
}

func (f *FileInput) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusRunning {
		return nil
	}

	streams, err := ProbeFileStreams(f.config.Path)
	if err != nil {
		logger.Errorf("failed to probe file %s: %v", f.config.Path, err)
		f.status = StreamStatusError
		return err
	}

	f.streams = streams
	f.status = StreamStatusRunning
	ctx, f.cancel = context.WithCancel(ctx)

	logger.Infof("file input started: %s, file: %s, streams: %d", f.id, f.config.Path, len(streams))
	return nil
}

func (f *FileInput) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusStopped {
		return nil
	}

	if f.cancel != nil {
		f.cancel()
	}

	f.status = StreamStatusStopped
	logger.Infof("file input stopped: %s", f.id)
	return nil
}

func (f *FileInput) ReadPacket() (*MediaPacket, error) {
	return nil, ErrStreamNotRunning
}

func (f *FileInput) OnPacket(handler PacketHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}
