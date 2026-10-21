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

// TestRegisterResponseMatches 校验 REGISTER 响应必须按 Call-ID + CSeq 关联：
// 共享响应通道里的残留响应绝不能当成本次注册的成功应答（KDM 2026-10-21）。
func TestRegisterResponseMatches(t *testing.T) {
	resp := &SipMessage{CallID: "cur", CSeq: "  9   REGISTER ", StatusCode: 200}
	if !registerResponseMatches(resp, "cur", "9 REGISTER") {
		t.Fatal("matching Call-ID/CSeq (with whitespace) should match")
	}
	if registerResponseMatches(resp, "old", "9 REGISTER") {
		t.Fatal("different Call-ID must not match")
	}
	if registerResponseMatches(resp, "cur", "10 REGISTER") {
		t.Fatal("different CSeq must not match")
	}
	if registerResponseMatches(nil, "cur", "9 REGISTER") {
		t.Fatal("nil response must not match")
	}
}

// TestChannelResponseSourceDiscardsStale 旧响应应被丢弃，匹配的响应才返回。
func TestChannelResponseSourceDiscardsStale(t *testing.T) {
	s := New(Config{DeviceID: "34020000001320000001", SIPDomain: "3402000000"}, DeviceInfo{}, nil)
	s.regRespCh <- SipMessage{CallID: "old", CSeq: "7 REGISTER", StatusCode: 200}
	s.regRespCh <- SipMessage{CallID: "cur", CSeq: "9 REGISTER", StatusCode: 401}

	resp, err := s.channelResponseSource()(time.Second, "cur", "9 REGISTER")
	if err != nil {
		t.Fatalf("expected matching response, got error: %v", err)
	}
	if resp.CallID != "cur" || resp.StatusCode != 401 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	select {
	case extra := <-s.regRespCh:
		t.Fatalf("stale response should have been discarded, got %+v", extra)
	default:
	}
}

// TestChannelResponseSourceTimeoutOnMismatch 只有不匹配的响应时必须超时，
// 绝不能误判成功。
func TestChannelResponseSourceTimeoutOnMismatch(t *testing.T) {
	s := New(Config{DeviceID: "34020000001320000001", SIPDomain: "3402000000"}, DeviceInfo{}, nil)
	s.regRespCh <- SipMessage{CallID: "old", CSeq: "7 REGISTER", StatusCode: 200}

	start := time.Now()
	_, err := s.channelResponseSource()(120*time.Millisecond, "cur", "9 REGISTER")
	if err == nil {
		t.Fatal("expected timeout when only stale responses are present")
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("returned too early (%v), must keep waiting for the matching response", elapsed)
	}
}
