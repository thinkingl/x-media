package service

import (
	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// PipeService 管道服务
type PipeService struct {
	pipeRepo   repository.PipeRepository
	inputRepo  repository.InputRepository
	outputRepo repository.OutputRepository
	engine     media.MediaEngine
}

// NewPipeService 创建管道服务
func NewPipeService(
	pipeRepo repository.PipeRepository,
	inputRepo repository.InputRepository,
	outputRepo repository.OutputRepository,
	engine media.MediaEngine,
) *PipeService {
	return &PipeService{
		pipeRepo:   pipeRepo,
		inputRepo:  inputRepo,
		outputRepo: outputRepo,
		engine:     engine,
	}
}

// CreatePipeRequest 创建管道请求
type CreatePipeRequest struct {
	InputID  string `json:"input_id" binding:"required"`
	OutputID string `json:"output_id" binding:"required"`
}

// Create 创建管道
func (s *PipeService) Create(req *CreatePipeRequest) (*model.Pipe, error) {
	// 验证输入端存在
	_, err := s.inputRepo.GetByID(req.InputID)
	if err != nil {
		return nil, errors.NewNotFoundError("输入端", req.InputID)
	}

	// 验证输出端存在
	_, err = s.outputRepo.GetByID(req.OutputID)
	if err != nil {
		return nil, errors.NewNotFoundError("输出端", req.OutputID)
	}

	// 检查是否已存在相同的管道
	existingPipes, err := s.pipeRepo.GetByInputID(req.InputID)
	if err != nil {
		return nil, errors.NewInternalError(err)
	}
	for _, p := range existingPipes {
		if p.OutputID == req.OutputID {
			return nil, errors.NewValidationError("该管道已存在")
		}
	}

	pipe := &model.Pipe{
		ID:       utils.GenerateID(),
		InputID:  req.InputID,
		OutputID: req.OutputID,
		Status:   model.PipeStatusStopped,
	}

	if err := s.pipeRepo.Create(pipe); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return pipe, nil
}

// GetByID 根据ID获取管道
func (s *PipeService) GetByID(id string) (*model.Pipe, error) {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("管道", id)
	}
	return pipe, nil
}

// GetAll 获取所有管道
func (s *PipeService) GetAll() ([]model.Pipe, error) {
	return s.pipeRepo.GetAll()
}

// Delete 删除管道
func (s *PipeService) Delete(id string) error {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("管道", id)
	}

	if pipe.Status == model.PipeStatusRunning {
		return errors.NewValidationError("运行中的管道不能删除")
	}

	// 断开连接
	if err := s.engine.Disconnect(pipe.InputID, pipe.OutputID); err != nil {
		logger.Warnf("断开管道连接失败: %v", err)
	}

	return s.pipeRepo.Delete(id)
}

// Start 启动管道
func (s *PipeService) Start(id string) error {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("管道", id)
	}

	if pipe.Status == model.PipeStatusRunning {
		return errors.NewValidationError("管道已在运行中")
	}

	// 连接输入输出流
	if err := s.engine.Connect(pipe.InputID, pipe.OutputID); err != nil {
		return errors.NewInternalError(err)
	}

	// 更新状态
	return s.pipeRepo.UpdateStatus(id, model.PipeStatusRunning)
}

// Stop 停止管道
func (s *PipeService) Stop(id string) error {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("管道", id)
	}

	if pipe.Status == model.PipeStatusStopped {
		return errors.NewValidationError("管道已停止")
	}

	// 断开连接
	if err := s.engine.Disconnect(pipe.InputID, pipe.OutputID); err != nil {
		logger.Warnf("断开管道连接失败: %v", err)
	}

	// 更新状态
	return s.pipeRepo.UpdateStatus(id, model.PipeStatusStopped)
}
