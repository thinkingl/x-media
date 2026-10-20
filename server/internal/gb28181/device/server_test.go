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

// TestResponseCallIDUniquePerMessage 设备侧应答 MESSAGE 的 Call-ID 必须唯一：
// 同一秒内连续应答 Catalog/DeviceInfo/DeviceStatus 时，旧实现共用
// "<unix>-resp@<deviceID>" + "2 MESSAGE"，平台 SIP 栈回 482 Loop 丢弃后续应答
// （2026-10-20 KDM 启动阶段实测）。
func TestResponseCallIDUniquePerMessage(t *testing.T) {
	now := time.Now()
	a := responseCallID("32000000001120000286", "192.168.88.102", now)
	b := responseCallID("32000000001120000286", "192.168.88.102", now.Add(time.Nanosecond))
	if a == b {
		t.Fatalf("同秒连续应答的 Call-ID 必须不同: %q", a)
	}
	c := responseCallID("32000000001120000287", "192.168.88.102", now)
	if a == c {
		t.Fatalf("同秒不同设备的应答 Call-ID 必须不同: %q", a)
	}
}
