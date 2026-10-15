package media

// gb28181_hub.go —— 帧分发 hub：实现内置设备端的 FrameSource 接口。
//
// 语义（方案 §5.7）：
//   - 平台每次 INVITE 建立会话时 Subscribe（ctx 取消/BYE 时 Unsubscribe）；
//   - 每订阅者有界 channel，满则丢帧（关键帧优先、时间新鲜度）；
//   - M3 接入：标准帧 → device.AccessUnit（视频 Annex-B/NALU）与音频帧（AAC+ADTS），
//     以及新订阅先注入最近 GOP 的"GOP 重放"，保证平台秒级出图。

import (
	"context"
	"fmt"
	"sync"

	"github.com/x-media/x-media-server/internal/gb28181/device"
)

// gb28181SubCapacity 每订阅者 channel 容量（≈2s @ 视频 25fps + 音频 43fps）。
const gb28181SubCapacity = 128

type gb28181Sub struct {
	id string
	ch chan device.AccessUnit
}

// gb28181FrameHub 向平台会话分发 AccessUnit。
type gb28181FrameHub struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[string]*gb28181Sub
}

func newGB28181FrameHub() *gb28181FrameHub {
	return &gb28181FrameHub{subs: make(map[string]*gb28181Sub)}
}

// Subscribe 实现 device.FrameSource（INVITE 时由设备端调用）。
func (h *gb28181FrameHub) Subscribe(ctx context.Context) *device.FrameSubscription {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("sub-%d", h.nextID)
	sub := &gb28181Sub{id: id, ch: make(chan device.AccessUnit, gb28181SubCapacity)}
	h.subs[id] = sub
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.Unsubscribe(id)
	}()
	return &device.FrameSubscription{ID: id, Channel: sub.ch}
}

// Unsubscribe 实现 device.FrameSource（BYE/ctx 取消；幂等）。
func (h *gb28181FrameHub) Unsubscribe(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sub, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(sub.ch)
	}
}

// broadcast 非阻塞分发一帧（满则丢弃；调用方持有帧的只读引用）。
func (h *gb28181FrameHub) broadcast(au device.AccessUnit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub.ch <- au:
		default:
			// 订阅者消费慢：丢弃本帧（M3 起纳入丢帧计数/关键帧优先策略）。
		}
	}
}

// subscriberCount 当前平台会话数（供观测/测试）。
func (h *gb28181FrameHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
