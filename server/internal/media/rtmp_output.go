package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type RTMPOutput struct {
	mu     sync.RWMutex
	id     string
	config *OutputConfig
	status StreamStatus
	cancel context.CancelFunc
	ctx    context.Context
	muxer  *SimpleMuxer
}

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
		muxer:  NewSimpleMuxer(config.URL, "flv"),
	}, nil
}

func (r *RTMPOutput) ID() string           { return r.id }
func (r *RTMPOutput) Status() StreamStatus { r.mu.RLock(); defer r.mu.RUnlock(); return r.status }

func (r *RTMPOutput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.status = StreamStatusRunning
	logger.Infof("RTMP output ready: %s, URL: %s", r.id, r.config.URL)
	return nil
}

func (r *RTMPOutput) StartWithFile(ctx context.Context, filePath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.status = StreamStatusRunning
	if err := r.muxer.StartWithFile(r.ctx, filePath); err != nil {
		r.status = StreamStatusStopped
		return err
	}
	return nil
}

func (r *RTMPOutput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusStopped {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.muxer.Stop()
	r.status = StreamStatusStopped
	logger.Infof("RTMP output stopped: %s", r.id)
	return nil
}

func (r *RTMPOutput) WritePacket(pkt *MediaPacket) error {
	r.mu.RLock()
	status := r.status
	muxer := r.muxer
	r.mu.RUnlock()

	if status != StreamStatusRunning {
		return ErrStreamNotRunning
	}

	if !muxer.started {
		r.mu.Lock()
		if !muxer.started {
			if err := muxer.Start(r.ctx, pkt.CodecID); err != nil {
				r.mu.Unlock()
				return err
			}
		}
		r.mu.Unlock()
	}

	return muxer.WritePacket(pkt)
}

