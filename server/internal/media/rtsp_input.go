package media

import (
	"context"
	"sync"

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
		id:     id,
		config: config,
		status: StreamStatusStopped,
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

	streams, err := ProbeStreamsFromURL(r.config.URL)
	if err != nil {
		logger.Errorf("failed to probe RTSP %s: %v", r.config.URL, err)
		r.status = StreamStatusError
		return err
	}

	r.streams = streams
	r.status = StreamStatusRunning
	r.cancel = func() {}

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

func ProbeStreamsFromURL(url string) ([]StreamInfo, error) {
	demuxer := NewStreamDemuxer(url)
	return demuxer.Probe()
}
