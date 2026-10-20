package device

import (
	"strings"
	"testing"
	"time"
)

// TestKeepaliveCallIDUniquePerDevice 心跳 Call-ID 必须全局唯一：
// 同一进程内多台模拟设备在同一秒发心跳时，旧实现（keepalive-<Unix秒>@<本机IP>）
// 会生成完全相同的 Call-ID，平台 SIP 栈以 482 Loop Detected 丢弃心跳，设备
// 100 秒后被判离线（2026-10-20 科达 KDM 实机定位）。Call-ID 需按设备区分，
// 且连续心跳也需不同。
func TestKeepaliveCallIDUniquePerDevice(t *testing.T) {
	now := time.Now()
	a := keepaliveCallID("32000000001120000286", "192.168.88.102", now)
	b := keepaliveCallID("32000000001120000287", "192.168.88.102", now)
	if a == b {
		t.Fatalf("同秒不同设备的心跳 Call-ID 必须不同: %q", a)
	}
	c := keepaliveCallID("32000000001120000286", "192.168.88.102", now.Add(time.Nanosecond))
	if a == c {
		t.Fatalf("同一设备的连续心跳 Call-ID 必须不同: %q", a)
	}
	if !strings.Contains(a, "32000000001120000286") {
		t.Fatalf("Call-ID %q 应包含设备编码", a)
	}
}
