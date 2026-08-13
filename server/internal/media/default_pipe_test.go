package media

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleStreams() []StreamInfo {
	return []StreamInfo{
		{ChannelID: 0, Kind: "video", CodecID: CodecH264, CodecName: "H264", ClockRate: 90000},
		{ChannelID: 1, Kind: "audio", CodecID: CodecAAC, CodecName: "AAC", ClockRate: 32000},
	}
}

func testFrame(ch uint8, ft FrameType, codec CodecID, pts int64, payload []byte) *Frame {
	return &Frame{
		Header: FrameHeader{
			Magic:     FrameMagic,
			Version:   FrameVersion,
			ChannelID: ch,
			FrameType: ft,
			Codec:     codec,
			PTS:       pts,
			DTS:       pts,
		},
		Payload: payload,
	}
}

func TestPipe_ForwardsFrames(t *testing.T) {
	src := NewMockSource("src1", sampleStreams())
	sink := NewMockSink("sink1")
	pipe := NewDefaultPipe(128) // 足够大，无背压时全转发

	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, pipe.Start(ctx))
	defer func() { cancel(); pipe.Stop() }()

	// source 推帧
	for i := 0; i < 50; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, []byte{1, 2, 3}))
	}

	waitCond(t, 3*time.Second, func() bool {
		return sink.FrameCount() == 50
	}, "all frames forwarded")

	assert.Equal(t, int64(50), pipe.Written())
	assert.Equal(t, int64(0), pipe.Dropped())

	// 字节与顺序一致
	frames := sink.Frames()
	for i, f := range frames {
		assert.Equal(t, int64(i)*3000, f.Header.PTS, "frame %d PTS in order", i)
		assert.Equal(t, uint8(0), f.Header.ChannelID)
	}
}

func TestPipe_FanOutMultipleSinks(t *testing.T) {
	src := NewMockSource("srcfan", sampleStreams())
	sink1 := NewMockSink("fan1")
	sink2 := NewMockSink("fan2")

	p1 := NewDefaultPipe(64)
	p2 := NewDefaultPipe(64)
	require.NoError(t, p1.Bind(src, sink1))
	require.NoError(t, p2.Bind(src, sink2))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, p1.Start(ctx))
	require.NoError(t, p2.Start(ctx))
	defer p1.Stop()
	defer p2.Stop()

	for i := 0; i < 30; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i)*3000, nil))
	}

	waitCond(t, 3*time.Second, func() bool {
		return sink1.FrameCount() == 30 && sink2.FrameCount() == 30
	}, "both sinks receive all frames")

	assert.Equal(t, 30, sink1.FrameCount())
	assert.Equal(t, 30, sink2.FrameCount())
}

func TestPipe_SendSignalForwards(t *testing.T) {
	src := NewMockSource("srcsig", sampleStreams())
	sink := NewMockSink("sinksig")
	pipe := NewDefaultPipe(16)

	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	sig := mustSignal(t, SignalGetStreamInfo, 7, nil)
	reply, err := pipe.SendSignal(ctx, sig)
	require.NoError(t, err)
	require.True(t, reply.IsReply)
	require.Equal(t, uint64(7), reply.RequestID)

	// source 应记录收到信令
	assert.Equal(t, 1, len(src.Signals()))
	assert.Equal(t, SignalGetStreamInfo, src.Signals()[0].Type)
}

func TestPipe_SubscribeFlow(t *testing.T) {
	src := NewMockSource("srcsub", sampleStreams())
	sink := NewMockSink("sinksub")
	pipe := NewDefaultPipe(16)

	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// sink 通过 pipe 发 Subscribe
	req, err := NewSignal(SignalSubscribe, 1, &SubscribeRequest{Channels: []uint8{0}})
	require.NoError(t, err)
	reply, err := pipe.SendSignal(ctx, req)
	require.NoError(t, err)

	var resp SubscribeResponse
	require.NoError(t, reply.DecodePayload(&resp))
	assert.Len(t, resp.Streams, 2)

	assert.Equal(t, 1, src.SubscribeCount())
	assert.Equal(t, []uint8{0}, src.SubscribedChannels())
}

func TestPipe_BackpressureDropsFrames(t *testing.T) {
	src := NewMockSource("srcbp", sampleStreams())

	// 慢 sink：WriteFrame 阻塞
	slowSink := &blockingSink{id: "slow"}
	pipe := NewDefaultPipe(4) // 小缓冲

	require.NoError(t, pipe.Bind(src, slowSink))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, pipe.Start(ctx))
	defer pipe.Stop()

	// 推远超缓冲的帧，队列满后应丢帧且不阻塞 source
	for i := 0; i < 200; i++ {
		src.Push(testFrame(0, FrameTypeVideo, CodecH264, int64(i), nil))
	}

	// 稍等让消费线程尽力消费
	waitCond(t, 2*time.Second, func() bool {
		return pipe.Dropped() > 0
	}, "frames dropped under backpressure")

	assert.Greater(t, pipe.Dropped(), int64(0), "should drop frames when sink is slow")
}

func TestPipe_StopsCleanly(t *testing.T) {
	src := NewMockSource("srcstop", sampleStreams())
	sink := NewMockSink("sinkstop")
	pipe := NewDefaultPipe(16)

	require.NoError(t, pipe.Bind(src, sink))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, pipe.Start(ctx))

	src.Push(testFrame(0, FrameTypeVideo, CodecH264, 0, nil))
	time.Sleep(50 * time.Millisecond)

	pipe.Stop()
	cancel()

	// 停止后不再转发
	before := sink.FrameCount()
	src.Push(testFrame(0, FrameTypeVideo, CodecH264, 1, nil))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, before, sink.FrameCount(), "no frames after stop")
}

func TestPipe_BindWithoutStartNoCrash(t *testing.T) {
	src := NewMockSource("srcnb", sampleStreams())
	sink := NewMockSink("sinknb")
	pipe := NewDefaultPipe(16)
	require.NoError(t, pipe.Bind(src, sink))

	// 未 Start 直接推帧不应 panic/崩溃
	src.Push(testFrame(0, FrameTypeVideo, CodecH264, 0, nil))
}

func TestPipe_UnbindSignalToUnknown(t *testing.T) {
	pipe := NewDefaultPipe(16)
	_, err := pipe.SendSignal(context.Background(), mustSignal(t, SignalStart, 1, nil))
	assert.Error(t, err)
}

// blockingSink 模拟慢消费 sink。
type blockingSink struct {
	mu    sync.Mutex
	id    string
	stop  chan struct{}
	frames int
}

func (b *blockingSink) ID() string { return b.id }
func (b *blockingSink) Start(_ context.Context) error {
	b.stop = make(chan struct{})
	return nil
}
func (b *blockingSink) Stop() error { return nil }
func (b *blockingSink) Status() StreamStatus {
	return StreamStatusRunning
}
func (b *blockingSink) WriteFrame(f *Frame) error {
	// 模拟慢消费：每次写阻塞一小段时间
	select {
	case <-time.After(10 * time.Millisecond):
		b.mu.Lock()
		b.frames++
		b.mu.Unlock()
		return nil
	case <-b.stop:
		return nil
	}
}
func (b *blockingSink) Notify(sig *Signal) error { return nil }
func (b *blockingSink) Configure(streams []StreamInfo) error { return nil }
