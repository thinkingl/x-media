package media

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/x-media/x-media-server/pkg/logger"
)

// DefaultPipe 媒体管道标准实现。
//
// 数据面：source 帧回调 → 内部缓冲 → 独立消费 goroutine 写入 sink（带背压丢帧）。
// 控制面：SendSignal 同步转发到 source.Signal。
type DefaultPipe struct {
	source Source
	sink   Sink

	bufSize int

	mu      sync.RWMutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc

	inFlight chan *Frame // 待写入 sink 的缓冲队列
	stopped  atomic.Bool

	// 统计
	dropped atomic.Int64
	written atomic.Int64
}

// NewDefaultPipe 创建默认管道。bufSize 为每 sink 缓冲队列长度（帧数）。
func NewDefaultPipe(bufSize int) *DefaultPipe {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &DefaultPipe{bufSize: bufSize}
}

func (p *DefaultPipe) Bind(source Source, sink Sink) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if source == nil || sink == nil {
		return ErrInvalidConfig
	}
	p.source = source
	p.sink = sink
	source.AddFrameHandler(func(f *Frame) {
		p.routeFrame(f)
	})
	return nil
}

// routeFrame 数据面入口：尽量入队，队列满则丢帧并计数。
func (p *DefaultPipe) routeFrame(f *Frame) {
	p.mu.RLock()
	started := p.started
	inFlight := p.inFlight
	p.mu.RUnlock()

	if !started || inFlight == nil {
		return
	}
	select {
	case inFlight <- f:
	default:
		d := p.dropped.Add(1)
		if d%300 == 1 {
			logger.Warnf("pipe drop frame [%s -> %s] totalDropped=%d type=%d", p.source.ID(), p.sink.ID(), d, f.Header.FrameType)
		}
	}
}

func (p *DefaultPipe) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}
	if p.source == nil || p.sink == nil {
		return ErrInvalidConfig
	}

	ctx, p.cancel = context.WithCancel(ctx)
	p.ctx = ctx
	p.inFlight = make(chan *Frame, p.bufSize)
	p.started = true
	p.stopped.Store(false)

	// 协商：把 source 的媒体信息传给 sink 用于协议初始化。
	if streams, err := p.source.Streams(); err == nil {
		if err := p.sink.Configure(streams); err != nil {
			logger.Warnf("pipe configure failed %s -> %s: %v", p.source.ID(), p.sink.ID(), err)
		}
	} else {
		logger.Warnf("pipe streams fetch failed %s: %v", p.source.ID(), err)
	}

	go p.sinkWriter(ctx)
	logger.Infof("pipe started: %s -> %s (buf=%d)", p.source.ID(), p.sink.ID(), p.bufSize)
	return nil
}

// sinkWriter 消费缓冲并写入 sink。
func (p *DefaultPipe) sinkWriter(ctx context.Context) {
	var written int64
	exitReason := "context cancelled"
	defer func() {
		logger.Infof("pipe sinkWriter exited [%s -> %s] reason=%q written=%d", p.source.ID(), p.sink.ID(), exitReason, written)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-p.inFlight:
			if !ok {
				exitReason = "inFlight channel closed"
				return
			}
			if err := p.sink.WriteFrame(f); err != nil {
				logger.Warnf("pipe write failed %s -> %s: %v", p.source.ID(), p.sink.ID(), err)
				continue
			}
			written++
			if written%300 == 0 {
				logger.Infof("pipe heartbeat [%s -> %s] written=%d dropped=%d", p.source.ID(), p.sink.ID(), written, p.dropped.Load())
			}
			p.written.Add(1)
		}
	}
}

func (p *DefaultPipe) Stop() error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	p.started = false
	p.stopped.Store(true)
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
	logger.Infof("pipe stopped: %s -> %s (dropped=%d written=%d)", p.source.ID(), p.sink.ID(), p.dropped.Load(), p.written.Load())
	return nil
}

// SendSignal 控制面：sink→source 同步请求。
func (p *DefaultPipe) SendSignal(ctx context.Context, sig *Signal) (*Signal, error) {
	p.mu.RLock()
	source := p.source
	p.mu.RUnlock()
	if source == nil {
		return nil, ErrInputNotFound
	}

	switch sig.Type {
	case SignalSubscribe:
		var req SubscribeRequest
		if err := sig.DecodePayload(&req); err != nil {
			return nil, err
		}
		resp, err := source.Subscribe(ctx, &req)
		if err != nil {
			return nil, err
		}
		return NewReply(sig, resp)
	case SignalUnsubscribe:
		var channels []uint8
		if len(sig.Payload) > 0 {
			var req struct {
				Channels []uint8 `json:"channels,omitempty"`
			}
			if err := sig.DecodePayload(&req); err != nil {
				return nil, err
			}
			channels = req.Channels
		}
		if err := source.Unsubscribe(ctx, channels); err != nil {
			return nil, err
		}
		return NewReply(sig, nil)
	default:
		return source.Signal(ctx, sig)
	}
}

// Dropped 返回丢弃帧数。
func (p *DefaultPipe) Dropped() int64 { return p.dropped.Load() }

// Written 返回成功写入帧数。
func (p *DefaultPipe) Written() int64 { return p.written.Load() }
