package media

import (
	"context"
	"fmt"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type RTSPOutput struct {
	mu         sync.RWMutex
	id         string
	config     *OutputConfig
	status     StreamStatus
	cancel     context.CancelFunc
	ctx        context.Context
	muxer      *SimpleMuxer
	rtspServer *RTSPNativeServer
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
		target = fmt.Sprintf("rtsp://localhost%s/live", config.Addr)
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

func (r *RTSPOutput) StartWithFile(ctx context.Context, filePath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.status = StreamStatusRunning

	if r.config.Mode == "server" {
		r.rtspServer = NewRTSPNativeServer(r.config.Addr)
		if err := r.rtspServer.Start(r.ctx); err != nil {
			r.status = StreamStatusStopped
			return fmt.Errorf("failed to start RTSP server: %w", err)
		}
	}

	if err := r.muxer.StartWithFile(r.ctx, filePath); err != nil {
		if r.rtspServer != nil {
			r.rtspServer.Stop()
			r.rtspServer = nil
		}
		r.status = StreamStatusStopped
		return err
	}
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
	if r.rtspServer != nil {
		r.rtspServer.Stop()
		r.rtspServer = nil
	}
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

	if !muxer.IsStarted() {
		r.mu.Lock()
		if !muxer.IsStarted() {
			if err := muxer.Start(r.ctx, pkt.CodecID); err != nil {
				r.mu.Unlock()
				return err
			}
		}
		r.mu.Unlock()
	}

	return muxer.WritePacket(pkt)
}
