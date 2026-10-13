package media

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/stretchr/testify/require"
)

// TestRTSPSinkClientsConcurrent 复现 Clients() 锁外迭代 readers map 的竞态：
// OnPlay/OnSessionClose 持写锁增删 readers，而 Clients() 在 RUnlock 之后才
// 迭代同一 map —— 并发 map iteration + write 触发运行时 fatal（不可 recover）。
// 运行方式：go test -race ./internal/media -run TestRTSPSinkClientsConcurrent
func TestRTSPSinkClientsConcurrent(t *testing.T) {
	sink := newRTSPTestSink(t)
	defer sink.Stop()

	path := "live/" + sink.ID()
	handler := sink.handler
	require.NotNil(t, handler)

	handler.mutex.Lock()
	handler.paths[path] = &rtspPath{readers: make(map[*gortsplib.ServerSession]*rtspReader)}
	handler.mutex.Unlock()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 写侧：模拟 OnPlay/OnSessionClose 在写锁下增删 readers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			handler.mutex.Lock()
			p := handler.paths[path]
			if i%2 == 0 {
				p.readers[&gortsplib.ServerSession{}] = &rtspReader{
					address:   fmt.Sprintf("10.0.0.%d:554", i%255),
					transport: "tcp",
				}
			} else {
				for k := range p.readers {
					delete(p.readers, k)
					break
				}
			}
			handler.mutex.Unlock()
		}
	}()

	// 读侧：并发调用 Clients()（等价于前端轮询客户端列表接口）
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.After(300 * time.Millisecond)
			for {
				select {
				case <-stop:
					return
				case <-deadline:
					return
				default:
				}
				clients := sink.Clients()
				for _, c := range clients {
					_ = c.Address
				}
			}
		}()
	}

	time.Sleep(400 * time.Millisecond)
	close(stop)
	wg.Wait()
}
