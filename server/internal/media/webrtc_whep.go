package media

import (
	"io"
	"net/http"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/x-media/x-media-server/pkg/logger"
)

// webrtcWhepMu 串行化 WHEP 信令处理（pion PeerConnection 创建需加锁防并发竞态）。
var webrtcWhepMu sync.Mutex

// ServeWHEP 处理 WHEP 信令：接收浏览器 POST 的 SDP offer，建立 PeerConnection
// 并返回 SDP answer。协议遵循 WebRTC-HTTP Egress Protocol（WHEP）。
//
//	浏览器 POST /live/{outputID}/whep
//	  Content-Type: application/sdp
//	  Body: SDP offer（recvonly）
//	响应: 200 + SDP answer（Content-Type: application/sdp）
func (s *WebRTCSink) ServeWHEP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	if !ready {
		http.Error(w, "stream not ready", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(body),
	}

	webrtcWhepMu.Lock()
	pc, track, answer, err := s.negotiateWHEP(offer)
	webrtcWhepMu.Unlock()
	if err != nil {
		logger.Errorf("WebRTC WHEP negotiate failed [%s]: %v", s.id, err)
		http.Error(w, "webrtc negotiate failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 注册客户端；连接关闭时移除
	pair := s.newWebRTCTrackPair(track, remoteAddr(r))
	s.registerClient(pc, pair)
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.Infof("WebRTC peer [%s] state: %s", s.id, state)
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed {
			s.unregisterClient(pc)
			pair.stop()
		}
	})

	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(answer.SDP))
}

// negotiateWHEP 建立 PeerConnection，绑定视频发送轨道，返回 answer。
func (s *WebRTCSink) negotiateWHEP(offer webrtc.SessionDescription) (
	*webrtc.PeerConnection, *webrtc.TrackLocalStaticRTP, webrtc.SessionDescription, error,
) {
	config := webrtc.Configuration{}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, nil, webrtc.SessionDescription{}, err
	}

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeH264,
			ClockRate: 90000,
		},
		"video",
		s.id,
	)
	if err != nil {
		_ = pc.Close()
		return nil, nil, webrtc.SessionDescription{}, err
	}

	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return nil, nil, webrtc.SessionDescription{}, err
	}

	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = pc.Close()
		return nil, nil, webrtc.SessionDescription{}, err
	}

	// 创建 answer（iceCandidatePoolSize 固定，避免多客户端 ICE 竞态）
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, nil, webrtc.SessionDescription{}, err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return nil, nil, webrtc.SessionDescription{}, err
	}
	<-gatherComplete

	return pc, track, *pc.LocalDescription(), nil
}

// remoteAddr 从 HTTP 请求提取远端地址。
func remoteAddr(r *http.Request) string {
	if r == nil {
		return "?"
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return "?"
}
