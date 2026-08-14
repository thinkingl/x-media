package service

import (
	"encoding/json"
	"fmt"

	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// OutputService 输出端服务
type OutputService struct {
	repo      repository.OutputRepository
	engine    media.Engine
	pipeRepo  repository.PipeRepository
	inputRepo repository.InputRepository
}

// NewOutputService 创建输出端服务
func NewOutputService(repo repository.OutputRepository, engine media.Engine, pipeRepo repository.PipeRepository, inputRepo repository.InputRepository) *OutputService {
	return &OutputService{repo: repo, engine: engine, pipeRepo: pipeRepo, inputRepo: inputRepo}
}

// CreateOutputRequest 创建输出端请求
type CreateOutputRequest struct {
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type" binding:"required"`
	Config string `json:"config" binding:"required"`
}

// Create 创建输出端
func (s *OutputService) Create(req *CreateOutputRequest) (*model.Output, error) {
	// 验证类型
	if !isValidOutputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输出类型: %s", req.Type))
	}

	// 验证配置
	if err := validateOutputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	output := &model.Output{
		ID:     utils.GenerateID(),
		Name:   req.Name,
		Type:   req.Type,
		Config: req.Config,
		Status: model.OutputStatusStopped,
	}

	if err := s.repo.Create(output); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return output, nil
}

// GetByID 根据ID获取输出端
func (s *OutputService) GetByID(id string) (*model.Output, error) {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输出端", id)
	}
	return output, nil
}

// GetClients 返回指定输出端当前连接的客户端信息。
func (s *OutputService) GetClients(id string) ([]media.ClientInfo, error) {
	if _, err := s.repo.GetByID(id); err != nil {
		return nil, errors.NewNotFoundError("输出端", id)
	}
	return s.engine.GetOutputClients(id)
}

// GetAll 获取所有输出端
func (s *OutputService) GetAll() ([]model.Output, error) {
	return s.repo.GetAll()
}

// Update 更新输出端
func (s *OutputService) Update(id string, req *CreateOutputRequest) (*model.Output, error) {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("输出端", id)
	}

	// 验证类型
	if !isValidOutputType(req.Type) {
		return nil, errors.NewValidationError(fmt.Sprintf("不支持的输出类型: %s", req.Type))
	}

	// 验证配置
	if err := validateOutputConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	output.Name = req.Name
	output.Type = req.Type
	output.Config = req.Config

	if err := s.repo.Update(output); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return output, nil
}

// Delete 删除输出端
func (s *OutputService) Delete(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusRunning {
		return errors.NewValidationError("运行中的输出端不能删除")
	}

	// 从媒体引擎移除
	if err := s.engine.RemoveOutput(id); err != nil {
		logger.Warnf("从媒体引擎移除输出端失败: %v", err)
	}

	return s.repo.Delete(id)
}

// Start 启动输出端
func (s *OutputService) Start(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusRunning {
		return errors.NewValidationError("输出端已在运行中")
	}

	return startOutputOnly(s.engine, s.repo, id)
}

// Stop 停止输出端
func (s *OutputService) Stop(id string) error {
	output, err := s.repo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("输出端", id)
	}

	if output.Status == model.OutputStatusStopped {
		return errors.NewValidationError("输出端已停止")
	}

	// 从媒体引擎移除（同时断开其所有管道）
	if err := s.engine.RemoveOutput(id); err != nil {
		logger.Warnf("从媒体引擎移除输出端失败: %v", err)
	}

	// 更新状态
	if err := s.repo.UpdateStatus(id, model.OutputStatusStopped); err != nil {
		return err
	}

	// 级联：其管道全部置 stopped；相应输入若无其他运行管道引用则一并停止。
	pipes, err := s.pipeRepo.GetByOutputID(id)
	if err != nil {
		logger.Warnf("查询输出管道失败 %s: %v", id, err)
		return nil
	}
	for i := range pipes {
		p := &pipes[i]
		if p.Status != model.PipeStatusRunning {
			continue
		}
		if err := s.pipeRepo.UpdateStatus(p.ID, model.PipeStatusStopped); err != nil {
			logger.Warnf("更新管道状态失败 %s: %v", p.ID, err)
		}
		stopOrphanedInput(s.engine, s.inputRepo, s.pipeRepo, p.InputID)
	}
	return nil
}

// 辅助函数

func isValidOutputType(outputType string) bool {
	validTypes := map[string]bool{
		model.OutputTypeRTMP:    true,
		model.OutputTypeRTSP:    true,
		model.OutputTypeHTTPFLV: true,
		model.OutputTypeHLS:     true,
	}
	return validTypes[outputType]
}

// isValidRTSPTransport 校验 RTSP 传输协议配置值（auto/tcp/udp/udp-multicast）。
func isValidRTSPTransport(t string) bool {
	switch t {
	case "", media.RTSPTransportAuto, media.RTSPTransportTCP,
		media.RTSPTransportUDP, media.RTSPTransportUDPMulticast:
		return true
	}
	return false
}

func validateOutputConfig(outputType string, configStr string) error {
	var config model.OutputConfig
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return errors.NewValidationError("配置格式错误")
	}

	switch outputType {
	case model.OutputTypeRTMP:
		if config.URL == "" {
			return errors.NewValidationError("RTMP URL不能为空")
		}
	case model.OutputTypeRTSP:
		if config.Mode == "" {
			return errors.NewValidationError("RTSP模式不能为空")
		}
		if config.Mode == "push" && config.URL == "" {
			return errors.NewValidationError("推流模式URL不能为空")
		}
		if config.Mode == "server" && config.Addr == "" {
			return errors.NewValidationError("服务模式地址不能为空")
		}
		if config.Transport != "" && !isValidRTSPTransport(config.Transport) {
			return errors.NewValidationError("RTSP传输协议无效(可选: auto/tcp/udp/udp-multicast)")
		}
	case model.OutputTypeHTTPFLV:
		if config.Addr == "" {
			return errors.NewValidationError("HTTP-FLV地址不能为空")
		}
	}

	return nil
}
