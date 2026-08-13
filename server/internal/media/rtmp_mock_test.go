package media

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rtmpMockServer 最小 RTMP 服务端：完成握手 + connect/createStream/publish 流程，
// 记录收到的音视频消息供断言。约 200 行测试基建。
type rtmpMockServer struct {
	ln     net.Listener
	mu     sync.Mutex
	conns  []net.Conn
	msgs   []rtmpMockMsg
	closed bool
}

type rtmpMockMsg struct {
	Type    byte
	Ts      uint32
	Payload []byte
}

func startRTMPMockServer(t *testing.T) *rtmpMockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &rtmpMockServer{ln: ln}
	go s.acceptLoop()
	return s
}

func (s *rtmpMockServer) addr() string { return s.ln.Addr().String() }

func (s *rtmpMockServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *rtmpMockServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// 握手：收 C0+C1
	c0c1 := make([]byte, 1+rtmpHandshakeSize)
	if _, err := io.ReadFull(reader, c0c1); err != nil {
		return
	}
	if c0c1[0] != rtmpVersion {
		return
	}
	c1 := c0c1[1:]

	// 发 S0+S1+S2
	s0 := []byte{rtmpVersion}
	s1 := make([]byte, rtmpHandshakeSize)
	binary.BigEndian.PutUint32(s1[:4], uint32(time.Now().Unix()))
	s2 := append([]byte{}, c1...)
	conn.Write(append(append(s0, s1...), s2...))

	// 收 C2
	c2 := make([]byte, rtmpHandshakeSize)
	if _, err := io.ReadFull(reader, c2); err != nil {
		return
	}

	// 读消息循环
	chunkSizeIn := 128
	streamID := uint32(1)
	transactionID := 0.0
	for {
		msgType, ts, payload, err := readRTMPMessage(reader, chunkSizeIn)
		if err != nil {
			return
		}
		switch msgType {
		case msgTypeSetChunkSize:
			if len(payload) >= 4 {
				chunkSizeIn = int(binary.BigEndian.Uint32(payload))
			}
		case msgTypeAMF0Command:
			name, pos := parseAMF0String(payload, 0)
			switch name {
			case cmdConnect:
				transactionID, _ = parseAMF0Number(payload, pos)
				s.respondConnect(conn, transactionID)
			case cmdCreateStream:
				_, pos2 := parseAMF0String(payload, 0)
				transactionID, _ = parseAMF0Number(payload, pos2)
				s.respondCreateStream(conn, transactionID, 1)
			case cmdPublish:
				// 记录 publish
				s.record(msgType, ts, payload)
			default:
				s.record(msgType, ts, payload)
			}
		default:
			s.record(msgType, ts, payload)
		}
		_ = streamID
	}
}

func (s *rtmpMockServer) respondConnect(conn net.Conn, tx float64) {
	// Set Chunk Size = 4096（分块消息）
	scsPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(scsPayload, 4096)
	scsHdr := make([]byte, 12)
	scsHdr[0] = 2 // fmt=0 csid=2
	scsHdr[4] = 0
	scsHdr[5] = 0
	scsHdr[6] = 4
	scsHdr[7] = msgTypeSetChunkSize
	binary.LittleEndian.PutUint32(scsHdr[8:12], 0)
	conn.Write(append(scsHdr, scsPayload...))

	// Window Ack Size + Peer Bandwidth（可选，简单跳过）

	// _result(connect)
	payload := make([]byte, 0)
	payload = append(payload, amf0String("_result")...)
	payload = append(payload, amf0Number(tx)...)
	props, _ := encodeAMF0(amf0Object(map[string]any{
		"fmsVer":       "FMS/3,0,1,123",
		"capabilities": float64(31),
	}))
	payload = append(payload, props...)
	info, _ := encodeAMF0(amf0Object(map[string]any{
		"level":       "status",
		"code":        "NetConnection.Connect.Success",
		"description": "Connection succeeded",
	}))
	payload = append(payload, info...)
	conn.Write(rtmpCommandMessage(3, payload))
}

func (s *rtmpMockServer) respondCreateStream(conn net.Conn, tx float64, sid uint32) {
	payload := make([]byte, 0)
	payload = append(payload, amf0String("_result")...)
	payload = append(payload, amf0Number(tx)...)
	payload = append(payload, amf0Null()...)
	payload = append(payload, amf0Number(float64(sid))...)
	conn.Write(rtmpCommandMessage(3, payload))
}

func (s *rtmpMockServer) record(msgType byte, ts uint32, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	s.msgs = append(s.msgs, rtmpMockMsg{Type: msgType, Ts: ts, Payload: cp})
}

func (s *rtmpMockServer) messages() []rtmpMockMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.msgs
}

func (s *rtmpMockServer) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.ln.Close()
	for _, c := range s.conns {
		c.Close()
	}
}

// ---- 辅助 ----

// rtmpCommandMessage 构造 fmt=0 csid=3 的命令消息。
func rtmpCommandMessage(csid byte, payload []byte) []byte {
	hdr := make([]byte, 12)
	hdr[0] = csid & 0x3F
	hdr[1], hdr[2], hdr[3] = 0, 0, 0 // ts
	hdr[4] = byte(len(payload) >> 16)
	hdr[5] = byte(len(payload) >> 8)
	hdr[6] = byte(len(payload))
	hdr[7] = msgTypeAMF0Command
	binary.LittleEndian.PutUint32(hdr[8:12], 0) // stream id 0
	out := append(hdr, payload...)
	return out
}

// readRTMPMessage 从 reader 读一个完整 RTMP 消息（服务端视角，支持多块）。
func readRTMPMessage(r *bufio.Reader, chunkSize int) (byte, uint32, []byte, error) {
	base := make([]byte, 1)
	if _, err := io.ReadFull(r, base); err != nil {
		print("MOCK read base err:", err)
		return 0, 0, nil, err
	}
	fmtBits := base[0] >> 6
	csid := int(base[0] & 0x3F)
	if csid == 0 {
		ext := make([]byte, 1)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, 0, nil, err
		}
		csid = int(ext[0]) + 64
	} else if csid == 1 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, 0, nil, err
		}
		csid = int(ext[0]) + int(ext[1])*256 + 64
	}

	var msgType byte
	var ts uint32
	var msgLen uint32
	switch fmtBits {
	case 0:
		mb := make([]byte, 11)
		if _, err := io.ReadFull(r, mb); err != nil {
			return 0, 0, nil, err
		}
		ts = uint32(mb[0])<<16 | uint32(mb[1])<<8 | uint32(mb[2])
		msgLen = uint32(mb[3])<<16 | uint32(mb[4])<<8 | uint32(mb[5])
		msgType = mb[6]
	case 3:
		return 0, 0, nil, fmt.Errorf("fmt=3 without context")
	default:
		return 0, 0, nil, fmt.Errorf("unsupported fmt=%d", fmtBits)
	}

	payload := make([]byte, msgLen)
	pos := uint32(0)
	for pos < msgLen {
		n := uint32(chunkSize)
		if n > msgLen-pos {
			n = msgLen - pos
		}
		if _, err := io.ReadFull(r, payload[pos:pos+n]); err != nil {
			return 0, 0, nil, err
		}
		pos += n
		if pos < msgLen {
			extra := make([]byte, 1)
			if _, err := io.ReadFull(r, extra); err != nil {
				return 0, 0, nil, err
			}
		}
	}
	_ = csid
	return msgType, ts, payload, nil
}

func parseAMF0Number(b []byte, pos int) (float64, int) {
	if pos+9 > len(b) || b[pos] != 0x00 {
		return 0, pos
	}
	bits := binary.BigEndian.Uint64(b[pos+1 : pos+9])
	return math.Float64frombits(bits), pos + 9
}
