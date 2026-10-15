package media

// gb28181_hub.go —— 帧分发 hub：实现内置设备端的 FrameSource 接口。
//
// 语义（方案 §5.7）：
//   - 平台每次 INVITE 建立会话时 Subscribe（ctx 取消/BYE 时 Unsubscribe）；
//   - 每订阅者有界 channel，满则丢帧（背压不阻塞输入）；
//   - GOP 缓存重放：缓存自最近 IDR 起的 AccessUnit（含随行音频），
//     新订阅者先注入缓存——平台 INVITE 后立即收到 IDR 出图。

import (
	"context"
	"fmt"
	"sync"

	"github.com/x-media/x-media-server/internal/gb28181/device"
)

// gb28181SubCapacity 每订阅者 channel 容量（≈2s @ 视频 25fps + 音频 43fps）。
const gb28181SubCapacity = 128

// gb28181GOPMaxBytes GOP 缓存上限；超出则丢弃缓存、等待下一关键帧。
const gb28181GOPMaxBytes = 4 << 20

type gb28181Sub struct {
	id string
	ch chan device.AccessUnit
}

// gb28181FrameHub 向平台会话分发 AccessUnit。
type gb28181FrameHub struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[string]*gb28181Sub

	// GOP 缓存（自最近关键帧起；truncated 标记等待下一关键帧）
	gop          []device.AccessUnit
	gopBytes     int
	gopTruncated bool

	dropped int64 // 订阅者满导致的丢帧数（观测）
}

func newGB28181FrameHub() *gb28181FrameHub {
	return &gb28181FrameHub{subs: make(map[string]*gb28181Sub)}
}

// Subscribe 实现 device.FrameSource（INVITE 时由设备端调用）。
// 新订阅者先收到 GOP 缓存（若可用），再续实时帧。
func (h *gb28181FrameHub) Subscribe(ctx context.Context) *device.FrameSubscription {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("sub-%d", h.nextID)
	sub := &gb28181Sub{id: id, ch: make(chan device.AccessUnit, gb28181SubCapacity)}
	h.subs[id] = sub
	// GOP 重放：channel 刚建（空），非阻塞写入即缓存上限内全部帧。
	if !h.gopTruncated && len(h.gop) > 0 {
		for _, au := range h.gop {
			select {
			case sub.ch <- au:
			default:
			}
		}
	}
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

// broadcast 非阻塞分发一帧，并维护 GOP 缓存。
func (h *gb28181FrameHub) broadcast(au device.AccessUnit) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// GOP 缓存维护：关键帧重置；超预算丢弃并等待下一关键帧。
	if au.KeyFrame {
		h.gop = h.gop[:0]
		h.gopBytes = 0
		h.gopTruncated = false
	}
	if !h.gopTruncated {
		h.gop = append(h.gop, au)
		h.gopBytes += gb28181AUFootprint(au)
		if h.gopBytes > gb28181GOPMaxBytes {
			h.gop = nil
			h.gopBytes = 0
			h.gopTruncated = true
		}
	}

	for _, sub := range h.subs {
		select {
		case sub.ch <- au:
		default:
			h.dropped++
		}
	}
}

// subscriberCount 当前平台会话数（供观测/测试）。
func (h *gb28181FrameHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// gopFrames 当前 GOP 缓存帧数（供测试）。
func (h *gb28181FrameHub) gopFrames() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.gop)
}

func gb28181AUFootprint(au device.AccessUnit) int {
	n := 0
	for _, v := range au.NALUs {
		n += len(v.Data)
	}
	for _, a := range au.Audio {
		n += len(a.Data)
	}
	return n
}
