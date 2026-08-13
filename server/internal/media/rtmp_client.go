package media

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
)

// RTMP 协议常量。
const (
	rtmpVersion   = 0x03
	rtmpHandshakeSize = 1536

	rtmpChunkSizeDefault = 128
	rtmpChunkSizeNegotiated = 4096

	// 消息类型
	msgTypeSetChunkSize  = 1
	msgTypeAbortMessage  = 2
	msgTypeAck           = 3
	msgTypeUserControl   = 4
	msgTypeWindowAckSize = 5
	msgTypeSetPeerBandwidth = 6
	msgTypeAMF0Command   = 20
	msgTypeAMF0Data      = 18
	msgTypeAudio         = 8
	msgTypeVideo         = 9

	// 命令
	cmdConnect     = "connect"
	cmdCreateStream = "createStream"
	cmdPublish     = "publish"
	cmdReleaseStream = "releaseStream"
	cmdFCPublish   = "FCPublish"
	cmdOnMetaData  = "@setDataFrame"
)

// rtmpClient 原生 RTMP 推流客户端。
type rtmpClient struct {
	mu sync.Mutex

	conn    net.Conn
	reader  *bufio.Reader
	app     string
	tcURL   string
	streamKey string

	chunkSizeOut int
	chunkSizeIn  int
	streamID     uint32
	connected    bool
	closed       bool
}

// newRTMPClient 从 rtmp:// URL 解析并创建客户端（不连接）。
func newRTMPClient(rawURL string) (*rtmpClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse rtmp url: %w", err)
	}
	if u.Scheme != "rtmp" {
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	app := strings.TrimPrefix(u.Path, "/")
	parts := strings.SplitN(app, "/", 2)
	var streamKey string
	if len(parts) == 2 {
		app = parts[0]
		streamKey = parts[1]
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing rtmp host")
	}
	return &rtmpClient{
		app:          app,
		tcURL:        "rtmp://" + u.Host + "/" + app,
		streamKey:    streamKey,
		chunkSizeOut: rtmpChunkSizeDefault,
		chunkSizeIn:  rtmpChunkSizeDefault,
	}, nil
}

// Connect 建立 TCP 连接并完成握手 + connect + createStream + publish。
func (c *rtmpClient) Connect(addr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("rtmp connect %s: %w", addr, err)
	}
	c.conn = conn
	c.reader = bufio.NewReaderSize(conn, 64*1024)

	if err := c.handshake(); err != nil {
		conn.Close()
		return err
	}

	// 协商较大 chunk size，避免小消息分块。
	if err := c.writeChunkedMessage(2, msgTypeSetChunkSize, 0, []byte{0, 0, 16, 0}); err != nil {
		conn.Close()
		return err
	}
	c.chunkSizeOut = 4096

	// connect 命令
	if err := c.sendConnect(); err != nil {
		conn.Close()
		return err
	}
	if err := c.waitResult(1); err != nil {
		conn.Close()
		return fmt.Errorf("connect result: %w", err)
	}

	// createStream
	if err := c.sendCommand(cmdCreateStream, 3, nil, []any{float64(0)}); err != nil {
		conn.Close()
		return err
	}
	if err := c.waitResult(2); err != nil {
		conn.Close()
		return fmt.Errorf("createStream result: %w", err)
	}

	// publish
	if err := c.sendCommand(cmdPublish, 8, []any{float64(0), c.streamKey, "live"}, nil); err != nil {
		conn.Close()
		return err
	}

	c.connected = true
	logger.Infof("RTMP connected: %s, app=%s stream=%s", addr, c.app, c.streamKey)
	return nil
}

// handshake 完成 RTMP 握手。
func (c *rtmpClient) handshake() error {
	c0 := []byte{rtmpVersion}
	c1 := make([]byte, rtmpHandshakeSize)
	binary.BigEndian.PutUint32(c1[:4], uint32(time.Now().Unix()))
	_, _ = rand.Read(c1[8:])

	if _, err := c.conn.Write(append(c0, c1...)); err != nil {
		return fmt.Errorf("send c0c1: %w", err)
	}

	s0s1 := make([]byte, 1+rtmpHandshakeSize)
	if _, err := ioReadFull(c.reader, s0s1); err != nil {
		return fmt.Errorf("read s0s1: %w", err)
	}
	if s0s1[0] != rtmpVersion {
		return fmt.Errorf("unexpected rtmp version: %d", s0s1[0])
	}

	s2 := make([]byte, rtmpHandshakeSize)
	if _, err := ioReadFull(c.reader, s2); err != nil {
		return fmt.Errorf("read s2: %w", err)
	}

	// C2 = echo S1
	c2 := s0s1[1:]
	if _, err := c.conn.Write(c2); err != nil {
		return fmt.Errorf("send c2: %w", err)
	}
	return nil
}

// sendConnect 发送 connect 命令。
func (c *rtmpClient) sendConnect() error {
	obj := amf0Object(map[string]any{
		"app":           c.app,
		"type":          "nonprivate",
		"flashVer":      "FMLE/3.0 (compatible; xmedia)",
		"tcUrl":         c.tcURL,
		"fpad":          false,
		"capabilities":  float64(15),
		"audioCodecs":   float64(4071),
		"videoCodecs":   float64(252),
		"videoFunction": float64(1),
	})
	return c.sendCommand(cmdConnect, 3, nil, []any{obj})
}

// sendCommand 发送 AMF0 命令消息。
func (c *rtmpClient) sendCommand(name string, csid uint32, transactionArgs []any, args []any) error {
	payload := make([]byte, 0)
	payload = append(payload, amf0String(name)...)
	payload = append(payload, amf0Number(0)...) // transaction id
	payload = append(payload, amf0Null()...)
	for _, a := range transactionArgs {
		b, err := encodeAMF0(a)
		if err != nil {
			return err
		}
		payload = append(payload, b...)
	}
	for _, a := range args {
		b, err := encodeAMF0(a)
		if err != nil {
			return err
		}
		payload = append(payload, b...)
	}
	return c.writeChunkedMessage(csid, msgTypeAMF0Command, 0, payload)
}

// writeChunkedMessage 按 chunk size 编码并发送 RTMP 消息。
func (c *rtmpClient) writeChunkedMessage(csid uint32, msgType byte, ts uint32, payload []byte) error {
	// 消息头
	header := make([]byte, 0, 16)
	header = append(header, 0) // fmt=0, csid 3 以内用 2 字节? 简化: 用 csid 3 的 1 字节形式
	// fmt=0 (full header), csid 在 3-63 用单字节
	csidByte := byte(csid)
	if csid >= 64 {
		csidByte = 0 // 需要 2/3 字节扩展形式
		return fmt.Errorf("csid >= 64 not supported")
	}
	header[0] = byte(csidByte & 0x3F) // fmt=0
	header = append(header, byte(ts>>16), byte(ts>>8), byte(ts))
	header = append(header, byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)))
	header = append(header, msgType)
	header = append(header, 0, 0, 0, 0) // message stream ID (4 bytes, little endian)
	// message stream ID little endian
	binary.LittleEndian.PutUint32(header[len(header)-4:], 1)

	// 写入头 + 按 chunk size 分块
	buf := make([]byte, 0, len(header)+len(payload)+64)
	buf = append(buf, header...)
	chunk := c.chunkSizeOut
	pos := 0
	first := true
	for pos < len(payload) {
		if !first {
			// fmt=3 后续块
			buf = append(buf, byte(csidByte&0x3F)|0xC0)
		}
		n := chunk
		if n > len(payload)-pos {
			n = len(payload) - pos
		}
		buf = append(buf, payload[pos:pos+n]...)
		pos += n
		first = false
	}
	_, err := c.conn.Write(buf)
	return err
}

// waitResult 等待 _result 响应。
func (c *rtmpClient) waitResult(_ uint32) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgType, _, payload, err := c.readMessage()
		if err != nil {
			return err
		}
		switch msgType {
		case msgTypeAMF0Command:
			name, _ := parseAMF0String(payload, 0)
			if name == "_result" {
				return nil
			}
		case msgTypeSetChunkSize:
			if len(payload) >= 4 {
				c.chunkSizeIn = int(binary.BigEndian.Uint32(payload))
			}
		case msgTypeSetPeerBandwidth:
			// 服务端要求的新 chunk size 已在 SetChunkSize 里
		case msgTypeWindowAckSize:
		}
	}
	return fmt.Errorf("timeout waiting for _result")
}

// readMessage 读取一个完整的 RTMP 消息。
func (c *rtmpClient) readMessage() (msgType byte, ts uint32, payload []byte, err error) {
	base := make([]byte, 1)
	if _, err = ioReadFull(c.reader, base); err != nil {
		return 0, 0, nil, err
	}
	fmtBits := base[0] >> 6
	csid := int(base[0] & 0x3F)
	if csid == 0 {
		ext := make([]byte, 1)
		if _, err = ioReadFull(c.reader, ext); err != nil {
			return
		}
		csid = int(ext[0]) + 64
	} else if csid == 1 {
		ext := make([]byte, 2)
		if _, err = ioReadFull(c.reader, ext); err != nil {
			return
		}
		csid = int(ext[0]) + int(ext[1])*256 + 64
	}

	var msgLen, msgStreamID uint32
	switch fmtBits {
	case 0:
		mb := make([]byte, 11)
		if _, err = ioReadFull(c.reader, mb); err != nil {
			return
		}
		ts = uint32(mb[0])<<16 | uint32(mb[1])<<8 | uint32(mb[2])
		msgLen = uint32(mb[3])<<16 | uint32(mb[4])<<8 | uint32(mb[5])
		msgType = mb[6]
		msgStreamID = binary.BigEndian.Uint32(mb[7:11])
	case 3:
		return 0, 0, nil, fmt.Errorf("unexpected fmt=3 without context (csid=%d)", csid)
	default:
		return 0, 0, nil, fmt.Errorf("unsupported chunk fmt=%d", fmtBits)
	}
	_ = msgStreamID

	payload = make([]byte, msgLen)
	pos := uint32(0)
	chunk := c.chunkSizeIn
	for pos < msgLen {
		n := uint32(chunk)
		if n > msgLen-pos {
			n = msgLen - pos
		}
		if _, err = ioReadFull(c.reader, payload[pos:pos+n]); err != nil {
			return
		}
		pos += n
		if pos < msgLen {
			extra := make([]byte, 1)
			if _, err = ioReadFull(c.reader, extra); err != nil {
				return
			}
		}
	}
	return msgType, ts, payload, nil
}

// SendData 发送数据消息（audio/video/metadata），stream 数据。
func (c *rtmpClient) SendData(msgType byte, ts uint32, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.closed {
		return fmt.Errorf("rtmp not connected")
	}
	return c.writeChunkedMessage(6, msgType, ts, payload)
}

// Close 关闭连接。
func (c *rtmpClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.connected = false
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func ioReadFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
		if m == 0 {
			return n, fmt.Errorf("zero read")
		}
	}
	return n, nil
}

// ---- AMF0 编码 ----

type amf0Object map[string]any

func amf0String(s string) []byte {
	b := []byte{0x02}
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(s)))
	b = append(b, lenBuf[:]...)
	return append(b, s...)
}

func amf0Number(f float64) []byte {
	b := []byte{0x00}
	var fbuf [8]byte
	binary.BigEndian.PutUint64(fbuf[:], math.Float64bits(f))
	return append(b, fbuf[:]...)
}

func amf0Null() []byte { return []byte{0x05} }

func amf0Bool(b bool) []byte {
	if b {
		return []byte{0x01, 0x01}
	}
	return []byte{0x01, 0x00}
}

func encodeAMF0(v any) ([]byte, error) {
	switch t := v.(type) {
	case string:
		return amf0String(t), nil
	case float64:
		return amf0Number(t), nil
	case int:
		return amf0Number(float64(t)), nil
	case int32:
		return amf0Number(float64(t)), nil
	case int64:
		return amf0Number(float64(t)), nil
	case bool:
		return amf0Bool(t), nil
	case nil:
		return amf0Null(), nil
	case []any:
		b := []byte{0x0A}
		lenArr := make([]byte, 4)
		binary.BigEndian.PutUint32(lenArr, uint32(len(t)))
		b = append(b, lenArr...)
		for _, item := range t {
			ib, err := encodeAMF0(item)
			if err != nil {
				return nil, err
			}
			b = append(b, ib...)
		}
		b = append(b, 0x00, 0x00, 0x09) // object end marker
		return b, nil
	case amf0Object:
		b := []byte{0x03}
		for k, val := range t {
			b = append(b, amf0String(k)...)
			vb, err := encodeAMF0(val)
			if err != nil {
				return nil, err
			}
			b = append(b, vb...)
		}
		b = append(b, 0x00, 0x00, 0x09) // object end marker
		return b, nil
	case map[string]any:
		return encodeAMF0(amf0Object(t))
	default:
		return nil, fmt.Errorf("unsupported AMF0 type: %T", v)
	}
}

func parseAMF0String(b []byte, pos int) (string, int) {
	if pos+1 >= len(b) || b[pos] != 0x02 {
		return "", pos
	}
	l := int(b[pos+1])<<8 | int(b[pos+2])
	if pos+3+l > len(b) {
		return "", pos
	}
	return string(b[pos+3 : pos+3+l]), pos + 3 + l
}
