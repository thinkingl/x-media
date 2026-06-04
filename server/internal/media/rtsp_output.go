package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type RTSPOutput struct {
	mu     sync.RWMutex
	id     string
	config *OutputConfig
	status StreamStatus
	cancel context.CancelFunc
	ctx    context.Context
	muxer  *SimpleMuxer
}

func NewRTSPOutput(config *OutputConfig) (*RTSPOutput, error) {
	if config.Mode == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	target := config.URL
	if config.Mode == "server" {
		target = "rtsp://0.0.0.0" + config.Addr + "/live"
	}

	return &RTSPOutput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
		muxer:  NewSimpleMuxer(target, "rtsp"),
	}, nil
}

func (r *RTSPOutput) ID() string           { return r.id }
func (r *RTSPOutput) Status() StreamStatus { r.mu.RLock(); defer r.mu.RUnlock(); return r.status }

func (r *RTSPOutput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.status = StreamStatusRunning
	logger.Infof("RTSP output ready: %s, mode: %s", r.id, r.config.Mode)
	return nil
}

func (r *RTSPOutput) Stop() error {
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
	logger.Infof("RTSP output stopped: %s", r.id)
	return nil
}

func (r *RTSPOutput) WritePacket(pkt *MediaPacket) error {
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

