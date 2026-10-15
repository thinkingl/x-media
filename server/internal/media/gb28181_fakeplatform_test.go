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
	t          *testing.T
	conn       *net.UDPConn
	realm      string
	nonce      string
	deviceID   string
	password   string
	platformID string

	events chan fakeSIPEvent

	mu           sync.Mutex
	requests     []device.SipMessage
	deviceAddr   *net.UDPAddr
	responses    map[string]device.SipMessage // 设备应答（MESSAGE），按 CmdType
	registered   int
	keepalives   int
	deregisters  int
	authFailures int
	cseq         int
}

type fakeSIPEvent struct {
	kind    string // registered / keepalive / deregistered / cmd-response / device-response
	cmdType string
	msg     device.SipMessage
}

func newFakeSIPPlatform(t *testing.T, platformID, realm, deviceID, password string) *fakeSIPPlatform {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	p := &fakeSIPPlatform{
		t: t, conn: conn, realm: realm, nonce: "a1b2c3d4e5f6a7b8",
		deviceID: deviceID, password: password, platformID: platformID,
		events:    make(chan fakeSIPEvent, 64),
		responses: make(map[string]device.SipMessage),
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
		p.deviceAddr = addr
		p.requests = append(p.requests, msg)
		p.mu.Unlock()

		// 设备对平台请求的 SIP 应答（StatusCode>0，如 SUBSCRIBE/OPTIONS 的 200）：
		// 记录事件，不再回复（回复一个"对响应的响应"没有意义）。
		if msg.StatusCode > 0 {
			p.emit(fakeSIPEvent{kind: "device-response", msg: msg})
			continue
		}
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
		p.emit(fakeSIPEvent{kind: "deregistered", msg: msg})
		return
	}
	p.registered++
	p.mu.Unlock()
	p.emit(fakeSIPEvent{kind: "registered", msg: msg})
}

func (p *fakeSIPPlatform) handleMessage(msg device.SipMessage, addr *net.UDPAddr) {
	p.reply(addr, msg, 200, "")
	switch {
	case strings.Contains(msg.Body, "Keepalive"):
		p.mu.Lock()
		p.keepalives++
		p.mu.Unlock()
		p.emit(fakeSIPEvent{kind: "keepalive", msg: msg})
	case strings.Contains(msg.Body, "<Response"):
		cmd := extractSIPCmdType(msg.Body)
		p.mu.Lock()
		p.responses[cmd] = msg
		p.mu.Unlock()
		p.emit(fakeSIPEvent{kind: "cmd-response", cmdType: cmd, msg: msg})
	default:
		p.mu.Lock()
		p.keepalives++
		p.mu.Unlock()
		p.emit(fakeSIPEvent{kind: "keepalive", msg: msg})
	}
}

var sipCmdTypeAttrRe = regexp.MustCompile(`CmdType="([A-Za-z0-9]+)"`)
var sipCmdTypeElemRe = regexp.MustCompile(`<CmdType>([A-Za-z0-9]+)</CmdType>`)

// extractSIPCmdType 从 MANSCDP 报文体提取 CmdType（属性或元素形式）。
func extractSIPCmdType(body string) string {
	if m := sipCmdTypeAttrRe.FindStringSubmatch(body); len(m) == 2 {
		return m[1]
	}
	if m := sipCmdTypeElemRe.FindStringSubmatch(body); len(m) == 2 {
		return m[1]
	}
	return ""
}

// sendRequest 以平台身份向设备发送 SIP 请求（MESSAGE/SUBSCRIBE/OPTIONS 等）。
func (p *fakeSIPPlatform) sendRequest(t *testing.T, method, extraHeaders, body string) {
	t.Helper()
	p.mu.Lock()
	addr := p.deviceAddr
	p.mu.Unlock()
	require.NotNil(t, addr, "应先收到设备报文以获知其地址")

	p.mu.Lock()
	p.cseq++
	n := p.cseq
	p.mu.Unlock()

	localPort := p.conn.LocalAddr().(*net.UDPAddr).Port
	var b strings.Builder
	fmt.Fprintf(&b, "%s sip:%s@%s SIP/2.0\r\n", method, p.deviceID, p.realm)
	fmt.Fprintf(&b, "Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bKfake%d\r\n", localPort, n)
	fmt.Fprintf(&b, "From: <sip:%s@%s>;tag=fake%d\r\n", p.platformID, p.realm, n)
	fmt.Fprintf(&b, "To: <sip:%s@%s>\r\n", p.deviceID, p.realm)
	fmt.Fprintf(&b, "Call-ID: fake-%d@127.0.0.1\r\n", n)
	fmt.Fprintf(&b, "CSeq: %d %s\r\n", n, method)
	fmt.Fprintf(&b, "Contact: <sip:%s@127.0.0.1:%d>\r\n", p.platformID, localPort)
	b.WriteString(extraHeaders)
	if body != "" {
		b.WriteString("Content-Type: Application/MANSCDP+xml\r\n")
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(body))
	b.WriteString(body)

	_, err := p.conn.WriteToUDP([]byte(b.String()), addr)
	require.NoError(t, err)
}

// query 发送 MANSCDP 查询/控制 XML，并等待设备返回对应 CmdType 的 Response。
func (p *fakeSIPPlatform) query(t *testing.T, body string) fakeSIPEvent {
	t.Helper()
	cmd := extractSIPCmdType(body)
	require.NotEmpty(t, cmd, "请求体应包含 CmdType")
	p.sendRequest(t, "MESSAGE", "", body)
	return p.waitCmdResponse(t, cmd, 5*time.Second)
}

// waitCmdResponse 等待设备应答事件（按 CmdType 匹配）。
func (p *fakeSIPPlatform) waitCmdResponse(t *testing.T, cmd string, timeout time.Duration) fakeSIPEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-p.events:
			if ev.kind == "cmd-response" && ev.cmdType == cmd {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting device response CmdType=%q", cmd)
		}
	}
}

// waitMethodResponse 等待设备对指定方法的 SIP 应答（如 SUBSCRIBE/OPTIONS 的 200）。
func (p *fakeSIPPlatform) waitMethodResponse(t *testing.T, method string, timeout time.Duration) device.SipMessage {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-p.events:
			if ev.kind == "device-response" && strings.Contains(ev.msg.CSeq, method) {
				return ev.msg
			}
		case <-deadline:
			t.Fatalf("timeout waiting device SIP response to %q", method)
		}
	}
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

func (p *fakeSIPPlatform) emit(ev fakeSIPEvent) {
	select {
	case p.events <- ev:
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
	platform := newFakeSIPPlatform(t, platformID, realm, deviceID, password)
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
