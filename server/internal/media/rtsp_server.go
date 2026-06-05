package media

import (
	"fmt"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
)

type rtspPath struct {
	stream    *gortsplib.ServerStream
	publisher *gortsplib.ServerSession
}

type RTSPServerHandler struct {
	server *gortsplib.Server
	mutex  sync.RWMutex
	paths  map[string]*rtspPath
}

func NewRTSPServerHandler() *RTSPServerHandler {
	return &RTSPServerHandler{
		paths: make(map[string]*rtspPath),
	}
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
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for path, p := range h.paths {
		if p.publisher == ctx.Session {
			p.stream.Close()
			delete(h.paths, path)
			logger.Infof("RTSP publisher disconnected: %s", path)
			return
		}
	}
}

func (h *RTSPServerHandler) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (
	*base.Response, *gortsplib.ServerStream, error,
) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	path := ctx.Path
	p, ok := h.paths[path]
	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

func (h *RTSPServerHandler) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (
	*base.Response, error,
) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	path := ctx.Path
	if p, ok := h.paths[path]; ok {
		p.stream.Close()
		if p.publisher != nil {
			p.publisher.Close()
		}
		delete(h.paths, path)
		logger.Infof("RTSP: replaced existing publisher on %s", path)
	}

	stream := &gortsplib.ServerStream{
		Server: h.server,
		Desc:   ctx.Description,
	}
	if err := stream.Initialize(); err != nil {
		return &base.Response{StatusCode: base.StatusInternalServerError}, err
	}

	h.paths[path] = &rtspPath{
		stream:    stream,
		publisher: ctx.Session,
	}

	logger.Infof("RTSP publisher connected: %s, streams: %d", path, len(ctx.Description.Medias))
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

	path := ctx.Path
	p, ok := h.paths[path]
	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
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
	path := ctx.Path

	ctx.Session.OnPacketRTPAny(func(medi *description.Media, _ format.Format, pkt *rtp.Packet) {
		h.mutex.RLock()
		defer h.mutex.RUnlock()

		if p, ok := h.paths[path]; ok {
			p.stream.WritePacketRTP(medi, pkt)
		}
	})

	logger.Infof("RTSP publisher started recording: %s", path)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

type RTSPServerManager struct {
	mu      sync.RWMutex
	servers map[string]*gortsplib.Server
	handler map[string]*RTSPServerHandler
}

var globalRTSPManager = &RTSPServerManager{
	servers: make(map[string]*gortsplib.Server),
	handler: make(map[string]*RTSPServerHandler),
}

func (m *RTSPServerManager) GetOrCreate(addr string) (*RTSPServerHandler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.handler[addr]; ok {
		return h, nil
	}

	h := NewRTSPServerHandler()
	srv := &gortsplib.Server{
		Handler:     h,
		RTSPAddress: addr,
	}
	h.SetServer(srv)

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("failed to start RTSP server on %s: %w", addr, err)
	}

	m.servers[addr] = srv
	m.handler[addr] = h
	logger.Infof("RTSP server started on %s", addr)
	return h, nil
}

func (m *RTSPServerManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for addr, srv := range m.servers {
		srv.Close()
		logger.Infof("RTSP server stopped on %s", addr)
	}
	m.servers = make(map[string]*gortsplib.Server)
	m.handler = make(map[string]*RTSPServerHandler)
}
