package media

import (
	"context"
	"fmt"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
)

type RTSPServerHandler struct {
	server    *gortsplib.Server
	mutex     sync.RWMutex
	stream    *gortsplib.ServerStream
	publisher *gortsplib.ServerSession
}

func NewRTSPServerHandler() *RTSPServerHandler {
	return &RTSPServerHandler{}
}

func (h *RTSPServerHandler) SetServer(s *gortsplib.Server) {
	h.server = s
}

func (h *RTSPServerHandler) OnConnOpen(_ *gortsplib.ServerHandlerOnConnOpenCtx) {
	logger.Debugf("RTSP connection opened")
}

func (h *RTSPServerHandler) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	logger.Debugf("RTSP connection closed: %v", ctx.Error)
}

func (h *RTSPServerHandler) OnSessionOpen(_ *gortsplib.ServerHandlerOnSessionOpenCtx) {
	logger.Debugf("RTSP session opened")
}

func (h *RTSPServerHandler) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	logger.Debugf("RTSP session closed")

	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.stream != nil && ctx.Session == h.publisher {
		h.stream.Close()
		h.stream = nil
		h.publisher = nil
		logger.Infof("RTSP publisher disconnected, stream closed")
	}
}

func (h *RTSPServerHandler) OnDescribe(_ *gortsplib.ServerHandlerOnDescribeCtx) (
	*base.Response, *gortsplib.ServerStream, error,
) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if h.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *RTSPServerHandler) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (
	*base.Response, error,
) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.stream != nil {
		h.stream.Close()
		if h.publisher != nil {
			h.publisher.Close()
		}
		h.stream = nil
		h.publisher = nil
		logger.Infof("RTSP: replaced existing publisher")
	}

	h.stream = &gortsplib.ServerStream{
		Server: h.server,
		Desc:   ctx.Description,
	}
	if err := h.stream.Initialize(); err != nil {
		return &base.Response{StatusCode: base.StatusInternalServerError}, err
	}
	h.publisher = ctx.Session

	logger.Infof("RTSP publisher connected, streams: %d", len(ctx.Description.Medias))
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *RTSPServerHandler) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (
	*base.Response, *gortsplib.ServerStream, error,
) {
	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if h.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *RTSPServerHandler) OnPlay(_ *gortsplib.ServerHandlerOnPlayCtx) (
	*base.Response, error,
) {
	logger.Debugf("RTSP reader started playing")
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *RTSPServerHandler) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (
	*base.Response, error,
) {
	ctx.Session.OnPacketRTPAny(func(medi *description.Media, _ format.Format, pkt *rtp.Packet) {
		h.mutex.RLock()
		defer h.mutex.RUnlock()

		if h.stream != nil {
			h.stream.WritePacketRTP(medi, pkt)
		}
	})

	logger.Infof("RTSP publisher started recording")
	return &base.Response{StatusCode: base.StatusOK}, nil
}

type RTSPNativeServer struct {
	mu       sync.RWMutex
	handler  *RTSPServerHandler
	server   *gortsplib.Server
	addr     string
	pathName string
	cancel   context.CancelFunc
}

func NewRTSPNativeServer(addr string) *RTSPNativeServer {
	return &RTSPNativeServer{
		addr: addr,
	}
}

func (s *RTSPNativeServer) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return fmt.Errorf("RTSP server already running")
	}

	s.handler = NewRTSPServerHandler()
	s.server = &gortsplib.Server{
		Handler:     s.handler,
		RTSPAddress: s.addr,
	}
	s.handler.SetServer(s.server)

	if err := s.server.Start(); err != nil {
		s.server = nil
		s.handler = nil
		return fmt.Errorf("failed to start RTSP server on %s: %w", s.addr, err)
	}

	logger.Infof("RTSP native server started on %s", s.addr)
	return nil
}

func (s *RTSPNativeServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		s.server.Close()
		s.server = nil
		s.handler = nil
		logger.Infof("RTSP native server stopped on %s", s.addr)
	}
}

func (s *RTSPNativeServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.server != nil
}
