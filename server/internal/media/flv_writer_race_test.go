package media

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFLVWriterClocksConcurrent 复现 clocks map 并发读写：
// Configure（RTMPSink.Configure → SetClockRate）写 clocks，而旧 pipe 的
// sinkWriter goroutine 每帧经 EncodeTagData → clockOf 无锁读同一 map ——
// 管道重建窗口内触发 concurrent map read/write（运行时 fatal）。
// 运行方式：go test -race ./internal/media -run TestFLVWriterClocksConcurrent
func TestFLVWriterClocksConcurrent(t *testing.T) {
	w := NewFLVWriter()
	frame := testFrame(0, FrameTypeVideo, CodecH264, 3000,
		[]byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x00, 0x33})

	stop := make(chan struct{})
	done := make(chan struct{})

	// 写侧：模拟 Configure 注册时钟率
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			w.SetClockRate(0, 90000+i%2)
		}
	}()

	// 读侧：模拟旧 pipe 的单个 sinkWriter goroutine 每帧编码
	// （生产中每个 sink 同一时刻只有一个 sinkWriter 调用 EncodeTagData）
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _, _ = w.EncodeTagData(frame)
		}
	}()

	// 并发窗口：主 goroutine 只计时，编码仅由上述单个 goroutine 执行
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-done

	// 功能断言：并发结束后仍能读到最新注册值
	w.SetClockRate(7, 44100)
	require.Equal(t, 44100, w.clockOf(7, 0))
	require.Equal(t, 90000, w.clockOf(8, 90000), "未注册通道应回落到 fallback")
}
