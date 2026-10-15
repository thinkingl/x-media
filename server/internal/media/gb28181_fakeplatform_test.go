package media

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/x-media/x-media-server/internal/gb28181/device"
)

// fakeSIPPlatform 进程内最小 GB28181 平台（UDP）：
// REGISTER 摘要质询/校验（qop=auth）、MESSAGE 心跳应答、注销（Expires:0）观察。
// 供 L2 组件测试驱动 Gb28181Sink 的信令生命周期。
type fakeSIPPlatform struct {
	t        *testing.T
	conn     *net.UDPConn
	realm    string
	nonce    string
	deviceID string
	password string

	events chan fakeSIPEvent

	mu           sync.Mutex
	requests     []device.SipMessage
	registered   int
	keepalives   int
	deregisters  int
	authFailures int
}

type fakeSIPEvent struct {
	kind string // registered / keepalive / deregistered
	msg  device.SipMessage
}

func newFakeSIPPlatform(t *testing.T, realm, deviceID, password string) *fakeSIPPlatform {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	p := &fakeSIPPlatform{
		t: t, conn: conn, realm: realm, nonce: "a1b2c3d4e5f6a7b8",
		deviceID: deviceID, password: password,
		events: make(chan fakeSIPEvent, 64),
	}
	go p.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return p
}

func (p *fakeSIPPlatform) addr() (string, int) {
	a := p.conn.LocalAddr().(*net.UDPAddr)
	return a.IP.String(), a.Port
}

func (p *fakeSIPPlatform) serve() {
	buf := make([]byte, 8192)
	for {
		n, addr, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg, err := device.Parse(buf[:n])
		if err != nil {
			p.t.Logf("fake platform: parse error: %v", err)
			continue
		}
		p.mu.Lock()
		p.requests = append(p.requests, msg)
		p.mu.Unlock()
		switch msg.Method {
		case "REGISTER":
			p.handleRegister(msg, addr)
		case "MESSAGE":
			p.handleMessage(msg, addr)
		default:
			p.reply(addr, msg, 200, "")
		}
	}
}

func (p *fakeSIPPlatform) handleRegister(msg device.SipMessage, addr *net.UDPAddr) {
	if msg.Authorization == "" {
		p.reply(addr, msg, 401, fmt.Sprintf(
			`Digest realm="%s", nonce="%s", algorithm=MD5, qop="auth"`, p.realm, p.nonce))
		return
	}
	if err := p.verifyDigest(msg); err != nil {
		p.mu.Lock()
		p.authFailures++
		p.mu.Unlock()
		p.t.Logf("fake platform: digest reject: %v", err)
		p.reply(addr, msg, 403, "")
		return
	}
	p.reply(addr, msg, 200, "")
	p.mu.Lock()
	if strings.TrimSpace(msg.Expires) == "0" {
		p.deregisters++
		p.mu.Unlock()
		p.emit("deregistered", msg)
		return
	}
	p.registered++
	p.mu.Unlock()
	p.emit("registered", msg)
}

func (p *fakeSIPPlatform) handleMessage(msg device.SipMessage, addr *net.UDPAddr) {
	p.reply(addr, msg, 200, "")
	p.mu.Lock()
	p.keepalives++
	p.mu.Unlock()
	p.emit("keepalive", msg)
}

var fakeSIPAuthKV = regexp.MustCompile(`(\w+)="?([^",]+)"?`)

func (p *fakeSIPPlatform) verifyDigest(msg device.SipMessage) error {
	kv := map[string]string{}
	auth := strings.TrimSpace(msg.Authorization)
	auth = strings.TrimPrefix(auth, "Digest ")
	auth = strings.TrimPrefix(auth, "digest ")
	for _, m := range fakeSIPAuthKV.FindAllStringSubmatch(auth, -1) {
		kv[strings.ToLower(m[1])] = m[2]
	}
	if kv["username"] != p.deviceID {
		return fmt.Errorf("username=%q", kv["username"])
	}
	want := device.ComputeResponse(kv["username"], kv["realm"], p.password,
		kv["nonce"], kv["uri"], msg.Method, kv["algorithm"], kv["qop"], kv["nc"], kv["cnonce"])
	if kv["response"] != want {
		return fmt.Errorf("response mismatch: got=%s want=%s (uri=%q)", kv["response"], want, kv["uri"])
	}
	return nil
}

func (p *fakeSIPPlatform) reply(addr *net.UDPAddr, req device.SipMessage, code int, wwwAuth string) {
	reasons := map[int]string{200: "OK", 401: "Unauthorized", 403: "Forbidden"}
	var b strings.Builder
	fmt.Fprintf(&b, "SIP/2.0 %d %s\r\n", code, reasons[code])
	if req.Via != "" {
		fmt.Fprintf(&b, "Via: %s\r\n", req.Via)
	}
	fmt.Fprintf(&b, "From: %s\r\n", req.From)
	to := req.To
	if !strings.Contains(strings.ToLower(to), "tag=") {
		to += ";tag=faketag"
	}
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Call-ID: %s\r\n", req.CallID)
	fmt.Fprintf(&b, "CSeq: %s\r\n", req.CSeq)
	if wwwAuth != "" {
		fmt.Fprintf(&b, "WWW-Authenticate: %s\r\n", wwwAuth)
	}
	b.WriteString("Content-Length: 0\r\n\r\n")
	_, _ = p.conn.WriteToUDP([]byte(b.String()), addr)
}

func (p *fakeSIPPlatform) emit(kind string, msg device.SipMessage) {
	select {
	case p.events <- fakeSIPEvent{kind: kind, msg: msg}:
	default:
	}
}

func (p *fakeSIPPlatform) waitEvent(t *testing.T, kind string, timeout time.Duration) fakeSIPEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-p.events:
			if ev.kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting fake platform event %q", kind)
		}
	}
}

// TestGB28181Sink_RegisterKeepaliveDeregister L2 验收：
// 注册（401 质询→带摘要 200）→ 心跳（MESSAGE 200）→ 注销（Expires:0）。
func TestGB28181Sink_RegisterKeepaliveDeregister(t *testing.T) {
	const (
		realm      = "3402000000"
		deviceID   = "34020000001320000001"
		platformID = "34020000002000000001"
		password   = "12345678"
	)
	platform := newFakeSIPPlatform(t, realm, deviceID, password)
	ip, port := platform.addr()

	sink, err := NewGB28181Sink(&OutputConfig{
		ID:   "gb28181_test_register",
		Type: "gb28181",
		GB28181: &GB28181Config{
			PlatformIP:        ip,
			PlatformPort:      port,
			PlatformID:        platformID,
			DeviceID:          deviceID,
			SIPDomain:         realm,
			Password:          password,
			HeartbeatInterval: 1,
		},
	})
	require.NoError(t, err)
	require.Equal(t, StreamStatusStopped, sink.Status())

	require.NoError(t, sink.Start(context.Background()))
	t.Cleanup(func() { _ = sink.Stop() })
	require.Equal(t, StreamStatusRunning, sink.Status())

	// 注册：首包无认证 → 401 质询 → 摘要认证 → 200
	reg := platform.waitEvent(t, "registered", 5*time.Second)
	assert.Equal(t, fmt.Sprintf("sip:%s@%s", platformID, realm), reg.msg.RequestURI,
		"platform_id 应写入请求 URI（方案 §5.9）")
	assert.Contains(t, reg.msg.From, deviceID)

	platform.mu.Lock()
	reqs := append([]device.SipMessage(nil), platform.requests...)
	platform.mu.Unlock()
	require.GreaterOrEqual(t, len(reqs), 2, "应观察到未认证+已认证两次 REGISTER")
	assert.Empty(t, reqs[0].Authorization, "首包 REGISTER 不应带 Authorization")
	assert.NotEmpty(t, reqs[1].Authorization, "401 质询后应带 Authorization")

	// 心跳 MESSAGE（heartbeat_interval=1s）
	// 注：内置库输出 MANSCDP 属性形式 XML（<Notify CmdType="Keepalive" ...>），
	// 与元素形式等价，wvp/ZLM 均接受。
	ka := platform.waitEvent(t, "keepalive", 5*time.Second)
	assert.Contains(t, ka.msg.Body, "Keepalive")
	assert.Contains(t, ka.msg.Body, "<DeviceID>"+deviceID+"</DeviceID>")
	assert.Contains(t, ka.msg.Body, "<Status>OK</Status>")

	// 注销：Stop → REGISTER Expires:0
	require.NoError(t, sink.Stop())
	dereg := platform.waitEvent(t, "deregistered", 5*time.Second)
	assert.Equal(t, "0", strings.TrimSpace(dereg.msg.Expires))

	platform.mu.Lock()
	authFailures := platform.authFailures
	platform.mu.Unlock()
	assert.Zero(t, authFailures, "摘要认证不应失败")
	assert.Equal(t, StreamStatusStopped, sink.Status())
}
