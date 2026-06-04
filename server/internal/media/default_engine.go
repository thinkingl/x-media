package media

import (
	"context"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
)

// DefaultMediaEngine 默认媒体引擎实现
type DefaultMediaEngine struct {
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	inputs   map[string]InputStream
	outputs  map[string]OutputStream
	conns    map[string][]string // inputID -> []outputID
}

// NewMediaEngine 创建媒体引擎
func NewMediaEngine() *DefaultMediaEngine {
	return &DefaultMediaEngine{
		inputs:  make(map[string]InputStream),
		outputs: make(map[string]OutputStream),
		conns:   make(map[string][]string),
	}
}

// Start 启动引擎
func (e *DefaultMediaEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ctx, e.cancel = context.WithCancel(ctx)
	logger.Info("媒体引擎已启动")
	return nil
}

// Stop 停止引擎
func (e *DefaultMediaEngine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cancel != nil {
		e.cancel()
	}

	// 停止所有输入流
	for id, input := range e.inputs {
		if err := input.Stop(); err != nil {
			logger.Errorf("停止输入流失败 %s: %v", id, err)
		}
	}

	// 停止所有输出流
	for id, output := range e.outputs {
		if err := output.Stop(); err != nil {
			logger.Errorf("停止输出流失败 %s: %v", id, err)
		}
	}

	logger.Info("媒体引擎已停止")
	return nil
}

// CreateInput 创建输入流
func (e *DefaultMediaEngine) CreateInput(config *InputConfig) (InputStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var input InputStream
	var err error

	switch config.Type {
	case "file":
		input, err = NewFileInput(config)
	case "rtsp":
		input, err = NewRTSPInput(config)
	default:
		return nil, ErrUnsupportedType
	}

	if err != nil {
		return nil, err
	}

	e.inputs[config.ID] = input
	logger.Infof("创建输入流: %s, 类型: %s", config.ID, config.Type)
	return input, nil
}

// CreateOutput 创建输出流
func (e *DefaultMediaEngine) CreateOutput(config *OutputConfig) (OutputStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var output OutputStream
	var err error

	switch config.Type {
	case "rtmp":
		output, err = NewRTMPOutput(config)
	case "rtsp":
		output, err = NewRTSPOutput(config)
	case "http-flv":
		output, err = NewHTTPFLVOutput(config)
	default:
		return nil, ErrUnsupportedType
	}

	if err != nil {
		return nil, err
	}

	e.outputs[config.ID] = output
	logger.Infof("创建输出流: %s, 类型: %s", config.ID, config.Type)
	return output, nil
}

// Connect 连接输入输出流
func (e *DefaultMediaEngine) Connect(inputID, outputID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	input, ok := e.inputs[inputID]
	if !ok {
		return ErrInputNotFound
	}

	if _, ok := e.outputs[outputID]; !ok {
		return ErrOutputNotFound
	}

	existing := e.conns[inputID]
	for _, oid := range existing {
		if oid == outputID {
			return nil
		}
	}

	input.OnPacket(func(pkt *MediaPacket) {
		e.mu.RLock()
		outputIDs := make([]string, len(e.conns[inputID]))
		copy(outputIDs, e.conns[inputID])
		outs := make([]OutputStream, 0, len(outputIDs))
		for _, oid := range outputIDs {
			if out, ok := e.outputs[oid]; ok {
				outs = append(outs, out)
			}
		}
		e.mu.RUnlock()
		for _, out := range outs {
			if err := out.WritePacket(pkt); err != nil {
				logger.Errorf("write packet failed %s: %v", out.ID(), err)
			}
		}
	})

	e.conns[inputID] = append(e.conns[inputID], outputID)
	logger.Infof("connected input->output: %s -> %s", inputID, outputID)
	return nil
}

// Disconnect 断开连接
func (e *DefaultMediaEngine) Disconnect(inputID, outputID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	outputs, ok := e.conns[inputID]
	if !ok {
		return ErrInputNotFound
	}

	// 移除连接
	for i, id := range outputs {
		if id == outputID {
			e.conns[inputID] = append(outputs[:i], outputs[i+1:]...)
			break
		}
	}

	logger.Infof("断开输入输出流连接: %s -> %s", inputID, outputID)
	return nil
}

// GetInput 获取输入流
func (e *DefaultMediaEngine) GetInput(id string) (InputStream, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	input, ok := e.inputs[id]
	if !ok {
		return nil, ErrInputNotFound
	}
	return input, nil
}

// GetOutput 获取输出流
func (e *DefaultMediaEngine) GetOutput(id string) (OutputStream, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	output, ok := e.outputs[id]
	if !ok {
		return nil, ErrOutputNotFound
	}
	return output, nil
}

// RemoveInput 移除输入流
func (e *DefaultMediaEngine) RemoveInput(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	input, ok := e.inputs[id]
	if !ok {
		return ErrInputNotFound
	}

	if err := input.Stop(); err != nil {
		logger.Errorf("停止输入流失败 %s: %v", id, err)
	}

	delete(e.inputs, id)
	delete(e.conns, id)
	logger.Infof("移除输入流: %s", id)
	return nil
}

// RemoveOutput 移除输出流
func (e *DefaultMediaEngine) RemoveOutput(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	output, ok := e.outputs[id]
	if !ok {
		return ErrOutputNotFound
	}

	if err := output.Stop(); err != nil {
		logger.Errorf("停止输出流失败 %s: %v", id, err)
	}

	delete(e.outputs, id)
	logger.Infof("移除输出流: %s", id)
	return nil
}

// GetStats 获取统计信息
func (e *DefaultMediaEngine) GetStats() *MediaStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// TODO: 实现统计信息收集
	return &MediaStats{}
}
