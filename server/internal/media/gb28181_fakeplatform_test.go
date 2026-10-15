package media

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
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

	// M3 媒体面：点播 RTP 接收
	rtpConn    *net.UDPConn
	rtpPackets []rtp.Packet // 按接收顺序（回环不重排）

	// M4：心跳静默模拟（忽略 MESSAGE 应答，验证静默→重注册）
	silent atomic.Bool

	// M4：NAT 模拟——响应 Via 回显 received/rport（外部映射地址），
	// 设备应学习并在后续 Contact 中使用。
	natExternalIP   string
	natExternalPort int

	// M4：TCP 媒体侧最近一次被接受的连接来源（KDM 匹配契约断言用）。
	tcpAcceptedRemote string
	// M4：最近一次 INVITE 的应答 SDP（KDM 匹配契约断言用）。
	lastInviteAnswer string
	// M4+：TCP 媒体组帧模式："dollar"（默认，国标 Annex C.2）或
	// "length"（RFC 4571 长度前缀，KDM/海康类平台实测行为）。
	tcpFraming string
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
		events:    make(chan fakeSIPEvent, 512),
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
	if p.silent.Load() {
		// 静默模拟：不回复 MESSAGE（心跳无应答）——验证设备端静默检测→重注册。
		return
	}
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

// sendRequest 以平台身份向设备发送 SIP 请求（MANSCDP MESSAGE/SUBSCRIBE/OPTIONS 等）。
func (p *fakeSIPPlatform) sendRequest(t *testing.T, method, extraHeaders, body string) {
	t.Helper()
	contentType := "Application/MANSCDP+xml"
	if body == "" {
		contentType = ""
	}
	p.sendRequestCT(t, method, extraHeaders, contentType, body)
}

// sendRequestCT 同上，指定 Content-Type（如 INVITE 的 application/sdp）。
func (p *fakeSIPPlatform) sendRequestCT(t *testing.T, method, extraHeaders, contentType, body string) {
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
	if contentType != "" {
		fmt.Fprintf(&b, "Content-Type: %s\r\n", contentType)
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

// waitMethodResponse 等待设备对指定方法的最终 SIP 应答（跳过 1xx 临时响应，
// 如 INVITE 的 100 Trying）。
func (p *fakeSIPPlatform) waitMethodResponse(t *testing.T, method string, timeout time.Duration) device.SipMessage {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-p.events:
			if ev.kind == "device-response" && strings.Contains(ev.msg.CSeq, method) && ev.msg.StatusCode >= 200 {
				return ev.msg
			}
		case <-deadline:
			t.Fatalf("timeout waiting device SIP response to %q", method)
		}
	}
}

// invite 发起点播（UDP；recvonly；y=ssrc）：分配 RTP 接收口 → INVITE →
// 校验 200 answer SDP → 回 ACK 并开始收包。返回媒体端口。
func (p *fakeSIPPlatform) invite(t *testing.T, ssrc uint32) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	// 高码率突发（1080p IDR 一次 100+ 包）需要更大的接收缓冲，
	// 200KB ≤ rmem_max(212992) 上限，避免回环丢包。
	_ = conn.SetReadBuffer(200 * 1024)
	p.mu.Lock()
	p.rtpConn = conn
	p.rtpPackets = nil
	p.mu.Unlock()
	go p.readRTP(conn)
	t.Cleanup(func() { _ = conn.Close() })

	mediaPort := conn.LocalAddr().(*net.UDPAddr).Port
	sdpBody := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=video %d RTP/AVP 96\r\na=recvonly\r\na=rtpmap:96 PS/90000\r\ny=%d\r\n",
		p.platformID, mediaPort, ssrc)
	p.sendRequestCT(t, "INVITE", "", "application/sdp", sdpBody)

	resp := p.waitMethodResponse(t, "INVITE", 5*time.Second)
	require.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Body, "a=sendonly", "answer SDP 必须为 sendonly")
	assert.Contains(t, resp.Body, "a=rtpmap:96 PS/90000")
	assert.Contains(t, resp.Body, fmt.Sprintf("y=%d", ssrc), "answer 应回显平台 SSRC")
	// 平台 ACK（设备侧无操作；媒体在 200 OK 后即开始）
	p.sendRequest(t, "ACK", "", "")
	return mediaPort
}

// bye 结束会话（断言设备回 200）。
func (p *fakeSIPPlatform) bye(t *testing.T) {
	t.Helper()
	p.sendRequest(t, "BYE", "", "")
	resp := p.waitMethodResponse(t, "BYE", 5*time.Second)
	require.Equal(t, 200, resp.StatusCode)
}

func (p *fakeSIPPlatform) readRTP(conn *net.UDPConn) {
	for {
		buf := make([]byte, 4096)
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		// pion Unmarshal 的 Payload 引用入参 buffer：必须拷贝，否则后续读
		// 会覆盖已存包的内容（测试自身的数据竞争）。
		pkt.Payload = append([]byte(nil), pkt.Payload...)
		p.mu.Lock()
		p.rtpPackets = append(p.rtpPackets, pkt)
		p.mu.Unlock()
	}
}

// waitRTP 等待至少 n 个 RTP 包，返回快照。
func (p *fakeSIPPlatform) waitRTP(t *testing.T, n int, timeout time.Duration) []rtp.Packet {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		p.mu.Lock()
		if len(p.rtpPackets) >= n {
			out := append([]rtp.Packet(nil), p.rtpPackets...)
			p.mu.Unlock()
			return out
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			p.mu.Lock()
			got := len(p.rtpPackets)
			p.mu.Unlock()
			t.Fatalf("timeout waiting %d RTP packets (got %d)", n, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *fakeSIPPlatform) rtpCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.rtpPackets)
}

func (p *fakeSIPPlatform) regCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.registered
}

func (p *fakeSIPPlatform) setSilent(v bool) { p.silent.Store(v) }

// setNAT 开启 NAT 模拟：此后所有响应的 Via 回显 received=<ip>;rport=<port>。
func (p *fakeSIPPlatform) setNAT(ip string, port int) {
	p.mu.Lock()
	p.natExternalIP = ip
	p.natExternalPort = port
	p.mu.Unlock()
}

// contactOf 返回设备最近一条请求中的 Contact（按方法过滤，空串表示无）。
func (p *fakeSIPPlatform) contactOf(method string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.requests) - 1; i >= 0; i-- {
		if p.requests[i].Method == method {
			return p.requests[i].Contact
		}
	}
	return ""
}

// contactsOf 返回指定方法的全部 Contact（请求顺序）。
func (p *fakeSIPPlatform) contactsOf(method string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for i := range p.requests {
		if p.requests[i].Method == method {
			out = append(out, p.requests[i].Contact)
		}
	}
	return out
}

// readRTPFramed 读取 TCP 媒体流（GB/T 28181 附录 C.2 $-帧：
// 0x24 | channel(1B) | 长度(2B 大端) | RTP 包），解析为 RTP 包收集。
// setTCPFraming 设置 TCP 媒体组帧模式（"dollar" 默认 / "length"）。
// 仅影响此后接受到的媒体连接。
func (p *fakeSIPPlatform) setTCPFraming(mode string) {
	p.mu.Lock()
	p.tcpFraming = mode
	p.mu.Unlock()
}

func (p *fakeSIPPlatform) readRTPFramed(conn net.Conn) {
	p.mu.Lock()
	framing := p.tcpFraming
	p.mu.Unlock()
	if framing == "length" {
		p.readRTPLengthPrefixed(conn)
		return
	}
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		if header[0] != 0x24 {
			return // 非 $-帧，协议错误，停止读取
		}
		n := int(header[2])<<8 | int(header[3])
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf); err != nil {
			continue
		}
		pkt.Payload = append([]byte(nil), pkt.Payload...)
		p.mu.Lock()
		p.rtpPackets = append(p.rtpPackets, pkt)
		p.mu.Unlock()
	}
}

// readRTPLengthPrefixed 按 RFC 4571 长度前缀解析（[2字节大端长度][RTP]），
// 与 KDM/海康类平台收流端一致。
func (p *fakeSIPPlatform) readRTPLengthPrefixed(conn net.Conn) {
	lenBuf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		n := int(lenBuf[0])<<8 | int(lenBuf[1])
		if n < 12 {
			return // 长度不足以容纳 RTP 头，协议错误
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf); err != nil {
			continue
		}
		pkt.Payload = append([]byte(nil), pkt.Payload...)
		p.mu.Lock()
		p.rtpPackets = append(p.rtpPackets, pkt)
		p.mu.Unlock()
	}
}

// inviteTCPPlatformListens 发起 TCP 媒体点播且**平台侧监听**（offer setup:passive
// 语义上表示平台被动收流；expectAnswer 为期望的设备侧 answer setup 值）。
// 用于正常被动协商（设备答 active 主动连）与 tcp_role 强制场景。
// 返回应答 SDP 中公告的媒体端口（用于断言"连接源端口==公告端口"的匹配契约）。
func (p *fakeSIPPlatform) inviteTCPPlatformListens(t *testing.T, ssrc uint32, offerSetup, expectAnswer string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	p.mu.Lock()
	p.rtpPackets = nil
	p.tcpAcceptedRemote = ""
	p.mu.Unlock()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.tcpAcceptedRemote = conn.RemoteAddr().String()
		p.mu.Unlock()
		p.readRTPFramed(conn)
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	body := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=video %d TCP/RTP/AVP 96\r\na=recvonly\r\na=setup:%s\r\na=connection:new\r\na=rtpmap:96 PS/90000\r\ny=%d\r\n",
		p.platformID, port, offerSetup, ssrc)
	p.sendRequestCT(t, "INVITE", "", "application/sdp", body)

	resp := p.waitMethodResponse(t, "INVITE", 5*time.Second)
	require.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Body, "TCP/RTP/AVP", "answer 必须回 TCP/RTP/AVP")
	assert.Contains(t, resp.Body, "a=setup:"+expectAnswer, "answer setup 角色")
	assert.Contains(t, resp.Body, fmt.Sprintf("y=%d", ssrc))
	p.mu.Lock()
	p.lastInviteAnswer = resp.Body
	p.mu.Unlock()
	p.sendRequest(t, "ACK", "", "")

	m := regexp.MustCompile(`m=video (\d+) TCP/RTP/AVP`).FindStringSubmatch(resp.Body)
	require.Len(t, m, 2, "answer 应携带媒体端口")
	advPort, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	return advPort
}

// tcpAcceptedRemoteAddr 返回 TCP 媒体侧最近接受的连接来源（空串=尚未接受）。
func (p *fakeSIPPlatform) tcpAcceptedRemoteAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tcpAcceptedRemote
}

// inviteAnswer 返回最近一次 INVITE 的应答 SDP（KDM 匹配契约断言用）。
func (p *fakeSIPPlatform) inviteAnswer() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastInviteAnswer
}

// inviteTCPDeviceListens 发起 TCP 媒体点播且**平台主动拨号设备**（offer setup:active）：
// 从 200 answer 解析设备监听端口并拨号收流。
func (p *fakeSIPPlatform) inviteTCPDeviceListens(t *testing.T, ssrc uint32) {
	t.Helper()
	p.mu.Lock()
	p.rtpPackets = nil
	p.mu.Unlock()

	// m= 端口在 active offer 下由设备忽略（设备自有被动端口，answer 中携带）
	body := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n"+
		"m=video 40000 TCP/RTP/AVP 96\r\na=recvonly\r\na=setup:active\r\na=connection:new\r\na=rtpmap:96 PS/90000\r\ny=%d\r\n",
		p.platformID, ssrc)
	p.sendRequestCT(t, "INVITE", "", "application/sdp", body)

	resp := p.waitMethodResponse(t, "INVITE", 5*time.Second)
	require.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Body, "a=setup:passive", "平台拨号场景设备应被动监听")
	m := regexp.MustCompile(`m=video (\d+) TCP/RTP/AVP`).FindStringSubmatch(resp.Body)
	require.Len(t, m, 2, "answer 应携带设备监听端口")
	port, err := strconv.Atoi(m[1])
	require.NoError(t, err)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	go p.readRTPFramed(conn)
	p.sendRequest(t, "ACK", "", "")
}

// assemblePS 按接收顺序拼接 RTP 负载（回环不重排）。
func assemblePS(pkts []rtp.Packet) []byte {
	var out []byte
	for i := range pkts {
		out = append(out, pkts[i].Payload...)
	}
	return out
}

var fakeSIPAuthKV = regexp.MustCompile(`(\w+)="?([^",]+)"?`)
var fakeSIPRportRe = regexp.MustCompile(`(?i);rport(=\d+)?`)

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
		via := req.Via
		p.mu.Lock()
		natIP, natPort := p.natExternalIP, p.natExternalPort
		p.mu.Unlock()
		if natIP != "" {
			// NAT 模拟：覆盖为外部映射地址（RFC 3581 received/rport 回显）
			via = fakeSIPRportRe.ReplaceAllString(via, "")
			via = strings.TrimSuffix(via, ";received=")
			via += fmt.Sprintf(";received=%s;rport=%d", natIP, natPort)
		}
		fmt.Fprintf(&b, "Via: %s\r\n", via)
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
	// 注：出站 MANSCDP 为**元素形式**（<Notify><CmdType>Keepalive</CmdType>…），
	// GB28181 规范形式；KDM 类平台解析器要求元素（属性形式会被判"未能解析"）。
	ka := platform.waitEvent(t, "keepalive", 5*time.Second)
	assert.Contains(t, ka.msg.Body, "<CmdType>Keepalive</CmdType>")
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
