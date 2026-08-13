package media

import (
	"context"
	"sync"
	"time"

	"github.com/stretchr/testify/require"
	"testing"
)

// MockSource 可编程媒体源：按剧本产帧、响应信令、记录收到的控制请求。
type MockSource struct {
	mu sync.Mutex

	id      string
	status  StreamStatus
	streams []StreamInfo
	handler FrameHandler
	handlers []FrameHandler

	started  bool
	stopped  bool
	subscribeCalls int
	subscribed     []uint8
	signals        []*Signal

	// pushEvents 预置的主动事件（如 InfoUpdate）
	events []*Signal
}

func NewMockSource(id string, streams []StreamInfo) *MockSource {
	return &MockSource{
		id:      id,
		status:  StreamStatusStopped,
		streams: streams,
	}
}

func (m *MockSource) ID() string { return m.id }

func (m *MockSource) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.status = StreamStatusRunning
	return nil
}

func (m *MockSource) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	m.status = StreamStatusStopped
	return nil
}

func (m *MockSource) Status() StreamStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *MockSource) WasStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

func (m *MockSource) WasStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

func (m *MockSource) Streams() ([]StreamInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams, nil
}

func (m *MockSource) SetStreams(s []StreamInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams = s
}

func (m *MockSource) Subscribe(_ context.Context, req *SubscribeRequest) (*SubscribeResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribeCalls++
	m.subscribed = req.Channels
	return &SubscribeResponse{Streams: m.streams}, nil
}

func (m *MockSource) SubscribeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.subscribeCalls
}

func (m *MockSource) SubscribedChannels() []uint8 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.subscribed
}

func (m *MockSource) Unsubscribe(_ context.Context, channels []uint8) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, &Signal{Type: SignalUnsubscribe})
	return nil
}

// Signal 记录收到的信令并按类型返回默认响应。
func (m *MockSource) Signal(_ context.Context, sig *Signal) (*Signal, error) {
	m.mu.Lock()
	m.signals = append(m.signals, sig)
	t := sig.Type
	m.mu.Unlock()

	switch t {
	case SignalGetStreamInfo:
		reply, err := NewReply(sig, &SubscribeResponse{Streams: m.streams})
		return reply, err
	case SignalStart, SignalPause, SignalResume, SignalStop, SignalSeek:
		reply, err := NewReply(sig, nil)
		return reply, err
	default:
		reply, err := NewReply(sig, nil)
		return reply, err
	}
}

func (m *MockSource) Signals() []*Signal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.signals
}

func (m *MockSource) SetFrameHandler(h FrameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = h
}

func (m *MockSource) AddFrameHandler(h FrameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, h)
}

// Push 主动向所有已注册帧回调推送一帧（模拟 demux 产出）。
func (m *MockSource) Push(f *Frame) {
	m.mu.Lock()
	hs := make([]FrameHandler, 0, len(m.handlers)+1)
	if m.handler != nil {
		hs = append(hs, m.handler)
	}
	hs = append(hs, m.handlers...)
	m.mu.Unlock()
	for _, h := range hs {
		h(f)
	}
}

// PushEvent 模拟 source 主动下发事件。
func (m *MockSource) PushEvent(sig *Signal) {
	m.mu.Lock()
	h := m.handler
	m.mu.Unlock()
	_ = h
	_ = sig
	// 事件经管道 Notify 下发，此处仅记录
	m.mu.Lock()
	m.events = append(m.events, sig)
	m.mu.Unlock()
}

// ---- MockSink ----

// MockSink 记录帧与事件、可发起信令请求。
type MockSink struct {
	mu sync.Mutex

	id     string
	status StreamStatus
	frames []*Frame
	events []*Signal
	streams []StreamInfo
	started bool
	stopped bool
}

func NewMockSink(id string) *MockSink {
	return &MockSink{id: id, status: StreamStatusStopped}
}

func (m *MockSink) ID() string { return m.id }

func (m *MockSink) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.status = StreamStatusRunning
	return nil
}

func (m *MockSink) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	m.status = StreamStatusStopped
	return nil
}

func (m *MockSink) Status() StreamStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *MockSink) WasStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

func (m *MockSink) WriteFrame(f *Frame) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, f)
	return nil
}

func (m *MockSink) Notify(sig *Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, sig)
	return nil
}

func (m *MockSink) Configure(streams []StreamInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams = streams
	return nil
}

func (m *MockSink) Streams() []StreamInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams
}

func (m *MockSink) Frames() []*Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frames
}

func (m *MockSink) FrameCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.frames)
}

func (m *MockSink) Events() []*Signal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events
}

// ---- MockPipe ----

// MockPipe 内存双向管道：真实 source 可推帧、真实 sink 可消费，信令 request/response 同步。
type MockPipe struct {
	mu sync.Mutex

	source Source
	sink   Sink

	started bool
	stopped bool
}

func NewMockPipe() *MockPipe {
	return &MockPipe{}
}

func (m *MockPipe) Bind(source Source, sink Sink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = source
	m.sink = sink
	// 接管 source 的帧回调：转发到 sink
	source.SetFrameHandler(func(f *Frame) {
		m.mu.Lock()
		sink := m.sink
		m.mu.Unlock()
		if sink != nil {
			_ = sink.WriteFrame(f)
		}
	})
	return nil
}

func (m *MockPipe) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *MockPipe) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}

func (m *MockPipe) SendSignal(ctx context.Context, sig *Signal) (*Signal, error) {
	m.mu.Lock()
	source := m.source
	m.mu.Unlock()
	if source == nil {
		return nil, ErrInputNotFound
	}
	return source.Signal(ctx, sig)
}

// ---- 辅助 ----

// mustSignal 构造信令并在测试中处理错误。
func mustSignal(t *testing.T, typ SignalType, id uint64, payload any) *Signal {
	t.Helper()
	s, err := NewSignal(typ, id, payload)
	require.NoError(t, err)
	return s
}

// waitCond 等待条件满足（替代 time.Sleep 猜测时序）。
func waitCond(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}
