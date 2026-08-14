package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
)

// MediaHub 媒体引擎：管理 Source/Sink 注册与 Pipe 连接。
//
// 取代旧 DefaultMediaEngine，是服务层的唯一入口：
//   - 输入注册为 Source，输出注册为 Sink
//   - 管道（pipe row）对应一个 Source↔Sink 连接（DefaultPipe）
//   - fan-out：同一 source 可被多个 sink 经独立 pipe 订阅
type MediaHub struct {
	mu      sync.RWMutex
	sources map[string]Source
	sinks   map[string]Sink
	pipes   map[string]*DefaultPipe // pipeKey = inputID + "->" + outputID

	ctx    context.Context
	cancel context.CancelFunc
}

// NewMediaHub 创建媒体引擎。
func NewMediaHub() *MediaHub {
	return &MediaHub{
		sources: make(map[string]Source),
		sinks:   make(map[string]Sink),
		pipes:   make(map[string]*DefaultPipe),
	}
}

// Start 启动引擎。
func (h *MediaHub) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctx, h.cancel = context.WithCancel(ctx)
	logger.Info("media hub started")
	return nil
}

// Stop 停止所有 source/sink/pipe。
func (h *MediaHub) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
	for _, s := range h.sources {
		_ = s.Stop()
	}
	for _, s := range h.sinks {
		_ = s.Stop()
	}
	for _, p := range h.pipes {
		p.Stop()
	}
	h.sources = make(map[string]Source)
	h.sinks = make(map[string]Sink)
	h.pipes = make(map[string]*DefaultPipe)
	logger.Info("media hub stopped")
	return nil
}

// CreateInput 创建输入 Source（兼容旧服务层接口）。
// 幂等：同 ID 已存在时直接返回既有实例，避免重复创建导致旧实例
// 的 readLoop 泄漏（旧实例会持续读文件并 emit，造成 CPU 争用与重复帧）。
func (h *MediaHub) CreateInput(config *InputConfig) (Source, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if src, ok := h.sources[config.ID]; ok {
		return src, nil
	}

	var src Source
	var err error
	switch config.Type {
	case "file":
		src, err = NewMP4Source(config)
	case "rtsp":
		src, err = NewRTSPInput(config)
	default:
		return nil, ErrUnsupportedType
	}
	if err != nil {
		return nil, err
	}
	h.sources[config.ID] = src
	logger.Infof("input created: %s, type: %s", config.ID, config.Type)
	return src, nil
}

// CreateOutput 创建输出 Sink（兼容旧服务层接口）。
// 幂等：同 ID 已存在时直接返回既有实例，避免重复创建导致旧实例
// 的 RTSP server 注册与 SDP 状态泄漏。
func (h *MediaHub) CreateOutput(config *OutputConfig) (Sink, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if sink, ok := h.sinks[config.ID]; ok {
		return sink, nil
	}

	var sink Sink
	var err error
	switch config.Type {
	case "rtmp":
		sink, err = NewRTMPSink(config)
	case "rtsp":
		sink, err = NewRTSPSink(config)
	case "http-flv":
		sink, err = NewHTTPFLVSink(config)
	default:
		return nil, ErrUnsupportedType
	}
	if err != nil {
		return nil, err
	}
	h.sinks[config.ID] = sink
	logger.Infof("output created: %s, type: %s", config.ID, config.Type)
	return sink, nil
}

// Connect 建立 source→sink 管道连接（兼容旧接口）。
func (h *MediaHub) Connect(inputID, outputID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	src, ok := h.sources[inputID]
	if !ok {
		return ErrInputNotFound
	}
	sink, ok := h.sinks[outputID]
	if !ok {
		return ErrOutputNotFound
	}

	key := pipeKey(inputID, outputID)
	if _, exists := h.pipes[key]; exists {
		return nil
	}

	pipe := NewDefaultPipe(1024)
	if err := pipe.Bind(src, sink); err != nil {
		return err
	}
	h.pipes[key] = pipe
	logger.Infof("pipe connected: %s -> %s", inputID, outputID)
	return nil
}

// Disconnect 断开管道连接（兼容旧接口）。
func (h *MediaHub) Disconnect(inputID, outputID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := pipeKey(inputID, outputID)
	if p, ok := h.pipes[key]; ok {
		p.Stop()
		delete(h.pipes, key)
		logger.Infof("pipe disconnected: %s -> %s", inputID, outputID)
	}
	return nil
}

// StartInput 启动输入 Source（兼容旧接口）。
func (h *MediaHub) StartInput(id string) error {
	h.mu.RLock()
	src, ok := h.sources[id]
	h.mu.RUnlock()
	if !ok {
		return ErrInputNotFound
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := src.Start(ctx); err != nil {
		logger.Errorf("input start failed: %s: %v", id, err)
		return err
	}
	logger.Infof("input started: %s", id)
	return nil
}

// StartOutput 启动输出 Sink（兼容旧接口）。
func (h *MediaHub) StartOutput(id string) error {
	h.mu.RLock()
	sink, ok := h.sinks[id]
	h.mu.RUnlock()
	if !ok {
		return ErrOutputNotFound
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := sink.Start(ctx); err != nil {
		logger.Errorf("output start failed: %s: %v", id, err)
		return err
	}
	logger.Infof("output started: %s", id)
	return nil
}

// StartOutputWithFile 兼容旧接口：文件输入时无需直读，直接启动 sink 即可。
func (h *MediaHub) StartOutputWithFile(id string, _ string) error {
	return h.StartOutput(id)
}

// StartPipe 启动一个已连接的管道。
func (h *MediaHub) StartPipe(inputID, outputID string) error {
	h.mu.RLock()
	pipe, ok := h.pipes[pipeKey(inputID, outputID)]
	h.mu.RUnlock()
	if !ok {
		return ErrInputNotFound
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := pipe.Start(ctx); err != nil {
		logger.Errorf("pipe start failed: %s -> %s: %v", inputID, outputID, err)
		return err
	}
	logger.Infof("pipe started: %s -> %s", inputID, outputID)
	return nil
}

// RemoveInput 移除输入 Source（兼容旧接口）。
func (h *MediaHub) RemoveInput(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	src, ok := h.sources[id]
	if !ok {
		return ErrInputNotFound
	}
	_ = src.Stop()

	// 断开所有相关管道
	for key, p := range h.pipes {
		if startsWith(key, id+"->") {
			p.Stop()
			delete(h.pipes, key)
		}
	}
	delete(h.sources, id)
	logger.Infof("input removed: %s", id)
	return nil
}

// RemoveOutput 移除输出 Sink（兼容旧接口）。
func (h *MediaHub) RemoveOutput(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	sink, ok := h.sinks[id]
	if !ok {
		return ErrOutputNotFound
	}
	_ = sink.Stop()

	for key, p := range h.pipes {
		if endsWith(key, "->"+id) {
			p.Stop()
			delete(h.pipes, key)
		}
	}
	delete(h.sinks, id)
	logger.Infof("output removed: %s", id)
	return nil
}

// GetOutput 获取输出 Sink（供 api server.go /live/:filename 使用）。
func (h *MediaHub) GetOutput(id string) (Sink, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	sink, ok := h.sinks[id]
	if !ok {
		return nil, ErrOutputNotFound
	}
	return sink, nil
}

// GetSink 获取输出 Sink 别名。
func (h *MediaHub) GetSink(id string) (Sink, error) { return h.GetOutput(id) }

// GetOutputClients 返回指定输出端当前连接的客户端信息。
func (h *MediaHub) GetOutputClients(id string) ([]ClientInfo, error) {
	sink, err := h.GetOutput(id)
	if err != nil {
		return nil, err
	}
	prov, ok := sink.(ClientInfoProvider)
	if !ok {
		return nil, nil // 该输出端类型不支持客户端信息
	}
	return prov.Clients(), nil
}

func pipeKey(inputID, outputID string) string {
	return inputID + "->" + outputID
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
