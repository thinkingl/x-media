package media

import (
	"context"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type RTSPInput struct {
	mu      sync.RWMutex
	id      string
	config  *InputConfig
	status  StreamStatus
	handler PacketHandler
	cancel  context.CancelFunc
	demuxer *StreamDemuxer
	streams []StreamInfo
}

func NewRTSPInput(config *InputConfig) (*RTSPInput, error) {
	if config.URL == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}
	return &RTSPInput{
		id:      id,
		config:  config,
		status:  StreamStatusStopped,
		demuxer: NewStreamDemuxer(config.URL),
	}, nil
}

func (r *RTSPInput) ID() string           { return r.id }
func (r *RTSPInput) Status() StreamStatus { r.mu.RLock(); defer r.mu.RUnlock(); return r.status }

func (r *RTSPInput) GetStreams() []StreamInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.streams
}

func (r *RTSPInput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusRunning {
		return nil
	}

	streams, err := r.demuxer.Probe()
	if err != nil {
		logger.Errorf("failed to probe RTSP stream %s: %v", r.config.URL, err)
		r.status = StreamStatusError
		return err
	}

	r.streams = streams
	r.status = StreamStatusRunning

	ctx, r.cancel = context.WithCancel(ctx)

	for _, stream := range streams {
		r.demuxer.OnPacket(stream.ChannelID, func(pkt *MediaPacket) {
			r.mu.RLock()
			handler := r.handler
			r.mu.RUnlock()
			if handler != nil {
				handler(pkt)
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

			if err := r.demuxer.Start(ctx); err != nil {
				logger.Errorf("RTSP demuxer start failed: %v", err)
				time.Sleep(3 * time.Second)
				continue
			}

			<-ctx.Done()

			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
				logger.Infof("RTSP input reconnect: %s", r.id)
			}
		}
	}()

	logger.Infof("RTSP input started: %s, url: %s, streams: %d", r.id, r.config.URL, len(streams))
	return nil
}

func (r *RTSPInput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status == StreamStatusStopped {
		return nil
	}

	if r.cancel != nil {
		r.cancel()
	}

	r.demuxer.Stop()
	r.status = StreamStatusStopped
	logger.Infof("RTSP input stopped: %s", r.id)
	return nil
}

func (r *RTSPInput) ReadPacket() (*MediaPacket, error) {
	return nil, ErrStreamNotRunning
}

func (r *RTSPInput) OnPacket(handler PacketHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

