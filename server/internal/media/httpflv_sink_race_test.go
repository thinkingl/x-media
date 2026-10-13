package media

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHTTPFLVSinkWriterConcurrent 复现 writer 指针无锁读写：
// Configure（持写锁）整体替换 h.writer，而 WriteFrame 只在 RLock 内取
// ready/status，解锁后才读 h.writer —— 与 Configure 并发时构成对同一
// 指针字段的无同步读写（管道重建窗口）。
// 运行方式：go test -race ./internal/media -run TestHTTPFLVSinkWriterConcurrent
func TestHTTPFLVSinkWriterConcurrent(t *testing.T) {
	sink := newFLVSink(t)
	defer sink.Stop()
	require.NoError(t, sink.Configure(flvTestStreams()))

	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, 0x00, 0x00, 0x00, 0x04,
	}
	frame := testFrame(0, FrameTypeVideo, CodecH264, 3000, frameData)

	stop := make(chan struct{})
	done := make(chan struct{})

	// 写侧：模拟管道重建时的反复 Configure（替换 writer）
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := sink.Configure(flvTestStreams()); err != nil {
				return
			}
		}
	}()

	// 读侧：模拟单个 sinkWriter 每帧编码（生产语义）
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = sink.WriteFrame(frame)
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-done

	// 功能断言：并发结束后仍能正常编码写入
	require.NoError(t, sink.WriteFrame(frame))
	sink.ring.mu.Lock()
	n := sink.ring.availableLocked()
	sink.ring.mu.Unlock()
	require.Greater(t, n, 0, "WriteFrame 应仍能写入环形缓冲")
}
