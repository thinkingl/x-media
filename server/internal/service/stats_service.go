package service

import (
	"github.com/x-media/x-media-server/internal/model"
	"github.com/x-media/x-media-server/internal/repository"
	"github.com/x-media/x-media-server/pkg/errors"
)

type StatsService struct {
	inputRepo  repository.InputRepository
	outputRepo repository.OutputRepository
	pipeRepo   repository.PipeRepository
}

func NewStatsService(
	inputRepo repository.InputRepository,
	outputRepo repository.OutputRepository,
	pipeRepo repository.PipeRepository,
) *StatsService {
	return &StatsService{
		inputRepo:  inputRepo,
		outputRepo: outputRepo,
		pipeRepo:   pipeRepo,
	}
}

func (s *StatsService) GetOverview() (*model.StatsResponse, error) {
	inputs, err := s.inputRepo.GetAll()
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	outputs, err := s.outputRepo.GetAll()
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	pipes, err := s.pipeRepo.GetAll()
	if err != nil {
		return nil, errors.NewInternalError(err)
	}

	var activeInputs, activeOutputs, activePipes int
	for _, i := range inputs {
		if i.Status == model.InputStatusRunning {
			activeInputs++
		}
	}
	for _, o := range outputs {
		if o.Status == model.OutputStatusRunning {
			activeOutputs++
		}
	}
	for _, p := range pipes {
		if p.Status == model.PipeStatusRunning {
			activePipes++
		}
	}

	return &model.StatsResponse{
		ActiveInputs:  activeInputs,
		ActiveOutputs: activeOutputs,
		ActivePipes:   activePipes,
	}, nil
}
