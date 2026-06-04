package media

import (
	"context"
	"sync"
	"time"

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
	demuxer *StreamDemuxer
	streams []StreamInfo
	bufCh   chan *MediaPacket
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
		id:      id,
		config:  config,
		status:  StreamStatusStopped,
		demuxer: NewStreamDemuxer(config.Path),
		bufCh:   make(chan *MediaPacket, 256),
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

	streams, err := f.demuxer.Probe()
	if err != nil {
		logger.Errorf("failed to probe file %s: %v", f.config.Path, err)
		f.status = StreamStatusError
		return err
	}

	f.streams = streams
	f.status = StreamStatusRunning

	ctx, f.cancel = context.WithCancel(ctx)

	for _, stream := range streams {
		f.demuxer.OnPacket(stream.ChannelID, func(pkt *MediaPacket) {
			f.mu.RLock()
			handler := f.handler
			bufCh := f.bufCh
			f.mu.RUnlock()
			if handler != nil {
				handler(pkt)
			}
			select {
			case bufCh <- pkt:
			default:
			}
		})
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := f.demuxer.Start(ctx); err != nil {
				logger.Errorf("demuxer start failed: %v", err)
				return
			}

			<-ctx.Done()

			if f.config.Loop {
				logger.Infof("file input loop: %s", f.id)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			break
		}
	}()

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

	f.demuxer.Stop()
	f.status = StreamStatusStopped
	logger.Infof("file input stopped: %s", f.id)
	return nil
}

func (f *FileInput) ReadPacket() (*MediaPacket, error) {
	f.mu.RLock()
	status := f.status
	bufCh := f.bufCh
	f.mu.RUnlock()

	if status != StreamStatusRunning {
		return nil, ErrStreamNotRunning
	}

	pkt, ok := <-bufCh
	if !ok {
		return nil, ErrStreamNotRunning
	}
	return pkt, nil
}

func (f *FileInput) OnPacket(handler PacketHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}
