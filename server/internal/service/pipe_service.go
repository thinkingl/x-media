package service

import (
	"context"
	"encoding/json"

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
	engine     media.Engine
}

// NewPipeService 创建管道服务
func NewPipeService(
	pipeRepo repository.PipeRepository,
	inputRepo repository.InputRepository,
	outputRepo repository.OutputRepository,
	engine media.Engine,
) *PipeService {
	return &PipeService{
		pipeRepo:   pipeRepo,
		inputRepo:  inputRepo,
		outputRepo: outputRepo,
		engine:     engine,
	}
}

type CreatePipeRequest struct {
	InputID    string `json:"input_id" binding:"required"`
	OutputID   string `json:"output_id" binding:"required"`
	ChannelMap string `json:"channel_map"`
	MuxSync    bool   `json:"mux_sync"`
}

func (s *PipeService) Update(id string, req *CreatePipeRequest) (*model.Pipe, error) {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return nil, errors.NewNotFoundError("管道", id)
	}

	if pipe.Status == model.PipeStatusRunning {
		return nil, errors.NewValidationError("运行中的管道不能修改")
	}

	if _, err := s.inputRepo.GetByID(req.InputID); err != nil {
		return nil, errors.NewNotFoundError("输入端", req.InputID)
	}
	if _, err := s.outputRepo.GetByID(req.OutputID); err != nil {
		return nil, errors.NewNotFoundError("输出端", req.OutputID)
	}

	pipe.InputID = req.InputID
	pipe.OutputID = req.OutputID
	pipe.ChannelMap = req.ChannelMap
	pipe.MuxSync = req.MuxSync

	if err := s.pipeRepo.Update(pipe); err != nil {
		return nil, errors.NewInternalError(err)
	}

	return pipe, nil
}

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
		ID:         utils.GenerateID(),
		InputID:    req.InputID,
		OutputID:   req.OutputID,
		ChannelMap: req.ChannelMap,
		MuxSync:    req.MuxSync,
		Status:     model.PipeStatusStopped,
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

func (s *PipeService) Start(id string) error {
	pipe, err := s.pipeRepo.GetByID(id)
	if err != nil {
		return errors.NewNotFoundError("管道", id)
	}

	if pipe.Status == model.PipeStatusRunning {
		return errors.NewValidationError("管道已在运行中")
	}

	if err := s.startPipe(pipe); err != nil {
		return err
	}

	// 同步上下游状态：输入/输出已随管道启动，DB 与引擎保持一致。
	if err := s.inputRepo.UpdateStatus(pipe.InputID, model.InputStatusRunning); err != nil {
		logger.Warnf("更新输入状态失败 %s: %v", pipe.InputID, err)
	}
	if err := s.outputRepo.UpdateStatus(pipe.OutputID, model.OutputStatusRunning); err != nil {
		logger.Warnf("更新输出状态失败 %s: %v", pipe.OutputID, err)
	}
	return s.pipeRepo.UpdateStatus(id, model.PipeStatusRunning)
}

// startPipe 执行管道引擎级启动（不做 DB 状态校验），供 Start 与启动恢复复用。
func (s *PipeService) startPipe(pipe *model.Pipe) error {
	input, err := s.inputRepo.GetByID(pipe.InputID)
	if err != nil {
		return errors.NewNotFoundError("输入端", pipe.InputID)
	}

	output, err := s.outputRepo.GetByID(pipe.OutputID)
	if err != nil {
		return errors.NewNotFoundError("输出端", pipe.OutputID)
	}

	var inputConfig model.InputConfig
	if err := json.Unmarshal([]byte(input.Config), &inputConfig); err != nil {
		return errors.NewValidationError("输入端配置格式错误")
	}

	inputMediaConfig := &media.InputConfig{
		ID:        input.ID,
		Type:      input.Type,
		Path:      inputConfig.Path,
		URL:       inputConfig.URL,
		Loop:      inputConfig.Loop != nil && *inputConfig.Loop,
		Transport: inputConfig.Transport,
	}

	if _, err := s.engine.CreateInput(inputMediaConfig); err != nil {
		if err != media.ErrUnsupportedType {
			logger.Warnf("创建输入流失败(可能已存在): %v", err)
		}
	}

	var outputConfig model.OutputConfig
	if err := json.Unmarshal([]byte(output.Config), &outputConfig); err != nil {
		return errors.NewValidationError("输出端配置格式错误")
	}

	outputMediaConfig := &media.OutputConfig{
		ID:        output.ID,
		Type:      output.Type,
		URL:       outputConfig.URL,
		Addr:      outputConfig.Addr,
		Mode:      outputConfig.Mode,
		Transport: outputConfig.Transport,
	}

	if _, err := s.engine.CreateOutput(outputMediaConfig); err != nil {
		if err != media.ErrUnsupportedType {
			logger.Warnf("创建输出流失败(可能已存在): %v", err)
		}
	}

	if err := s.engine.Connect(pipe.InputID, pipe.OutputID); err != nil {
		return errors.NewInternalError(err)
	}

	// 启动输出端（RTSP server 需先监听）、输入端，再启动管道数据面。
	if err := s.engine.StartOutput(pipe.OutputID); err != nil {
		logger.Warnf("启动输出流失败: %v", err)
	}
	if err := s.engine.StartInput(pipe.InputID); err != nil {
		logger.Warnf("启动输入流失败(可能已启动): %v", err)
	}
	if err := s.engine.StartPipe(pipe.InputID, pipe.OutputID); err != nil {
		// 配置失败（如 source 编码与 sink 不兼容）时返回错误，让前端/用户感知。
		return errors.NewInternalError(err)
	}
	return nil
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
	if err := s.pipeRepo.UpdateStatus(id, model.PipeStatusStopped); err != nil {
		return err
	}

	// 停止不再被任何运行管道引用的输入/输出，保持 DB 与引擎一致。
	stopOrphanedInput(s.engine, s.inputRepo, s.pipeRepo, pipe.InputID)
	stopOrphanedOutput(s.engine, s.outputRepo, s.pipeRepo, pipe.OutputID)
	return nil
}

// Restore 启动时根据 DB 中标记 running 的状态恢复媒体链路：
//   - 恢复所有运行中的管道（连带创建并启动其输入/输出）；
//   - 恢复无管道引用的独立运行输入/输出；
//   - 恢复失败的项目置为 stopped，避免下次重启重复尝试。
func (s *PipeService) Restore(ctx context.Context) error {
	pipes, err := s.pipeRepo.GetAll()
	if err != nil {
		return errors.NewInternalError(err)
	}
	for i := range pipes {
		p := &pipes[i]
		if p.Status != model.PipeStatusRunning {
			continue
		}
		if err := s.startPipe(p); err != nil {
			logger.Warnf("恢复管道失败 %s(%s->%s): %v", p.ID, p.InputID, p.OutputID, err)
			_ = s.pipeRepo.UpdateStatus(p.ID, model.PipeStatusStopped)
			stopOrphanedInput(s.engine, s.inputRepo, s.pipeRepo, p.InputID)
			stopOrphanedOutput(s.engine, s.outputRepo, s.pipeRepo, p.OutputID)
			continue
		}
		_ = s.inputRepo.UpdateStatus(p.InputID, model.InputStatusRunning)
		_ = s.outputRepo.UpdateStatus(p.OutputID, model.OutputStatusRunning)
	}

	inputs, err := s.inputRepo.GetAll()
	if err != nil {
		return errors.NewInternalError(err)
	}
	for i := range inputs {
		in := &inputs[i]
		if in.Status != model.InputStatusRunning || hasRunningPipeByInput(s.pipeRepo, in.ID) {
			continue
		}
		if err := startInputOnly(s.engine, s.inputRepo, in.ID); err != nil {
			logger.Warnf("恢复输入失败 %s: %v", in.ID, err)
			_ = s.inputRepo.UpdateStatus(in.ID, model.InputStatusStopped)
		}
	}

	outputs, err := s.outputRepo.GetAll()
	if err != nil {
		return errors.NewInternalError(err)
	}
	for i := range outputs {
		out := &outputs[i]
		if out.Status != model.OutputStatusRunning || hasRunningPipeByOutput(s.pipeRepo, out.ID) {
			continue
		}
		if err := startOutputOnly(s.engine, s.outputRepo, out.ID); err != nil {
			logger.Warnf("恢复输出失败 %s: %v", out.ID, err)
			_ = s.outputRepo.UpdateStatus(out.ID, model.OutputStatusStopped)
		}
	}
	return nil
}
