package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/x-media/x-media-server/internal/model"
)

// MockPipeRepo 模拟管道仓储
type MockPipeRepo struct {
	mock.Mock
}

func (m *MockPipeRepo) Create(pipe *model.Pipe) error {
	args := m.Called(pipe)
	return args.Error(0)
}

func (m *MockPipeRepo) GetByID(id string) (*model.Pipe, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Pipe), args.Error(1)
}

func (m *MockPipeRepo) GetAll() ([]model.Pipe, error) {
	args := m.Called()
	return args.Get(0).([]model.Pipe), args.Error(1)
}

func (m *MockPipeRepo) GetByInputID(inputID string) ([]model.Pipe, error) {
	args := m.Called(inputID)
	return args.Get(0).([]model.Pipe), args.Error(1)
}

func (m *MockPipeRepo) GetByOutputID(outputID string) ([]model.Pipe, error) {
	args := m.Called(outputID)
	return args.Get(0).([]model.Pipe), args.Error(1)
}

func (m *MockPipeRepo) Update(pipe *model.Pipe) error {
	args := m.Called(pipe)
	return args.Error(0)
}

func (m *MockPipeRepo) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockPipeRepo) UpdateStatus(id string, status string) error {
	args := m.Called(id, status)
	return args.Error(0)
}

func TestPipeService_Create(t *testing.T) {
	t.Run("成功创建管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		input := &model.Input{ID: "input_001", Name: "输入", Type: "file"}
		output := &model.Output{ID: "output_001", Name: "输出", Type: "rtmp"}

		req := &CreatePipeRequest{
			InputID:  "input_001",
			OutputID: "output_001",
		}

		mockInputRepo.On("GetByID", "input_001").Return(input, nil)
		mockOutputRepo.On("GetByID", "output_001").Return(output, nil)
		mockPipeRepo.On("GetByInputID", "input_001").Return([]model.Pipe{}, nil)
		mockPipeRepo.On("Create", mock.AnythingOfType("*model.Pipe")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "input_001", result.InputID)
		assert.Equal(t, "output_001", result.OutputID)
		assert.NotEmpty(t, result.ID)
		assert.Equal(t, model.PipeStatusStopped, result.Status)
		mockPipeRepo.AssertExpectations(t)
	})

	t.Run("输入端不存在", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		req := &CreatePipeRequest{
			InputID:  "nonexistent",
			OutputID: "output_001",
		}

		mockInputRepo.On("GetByID", "nonexistent").Return(nil, assert.AnError)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("输出端不存在", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		input := &model.Input{ID: "input_001", Name: "输入", Type: "file"}

		req := &CreatePipeRequest{
			InputID:  "input_001",
			OutputID: "nonexistent",
		}

		mockInputRepo.On("GetByID", "input_001").Return(input, nil)
		mockOutputRepo.On("GetByID", "nonexistent").Return(nil, assert.AnError)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("管道已存在", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		input := &model.Input{ID: "input_001", Name: "输入", Type: "file"}
		output := &model.Output{ID: "output_001", Name: "输出", Type: "rtmp"}
		existingPipe := model.Pipe{ID: "pipe_001", InputID: "input_001", OutputID: "output_001"}

		req := &CreatePipeRequest{
			InputID:  "input_001",
			OutputID: "output_001",
		}

		mockInputRepo.On("GetByID", "input_001").Return(input, nil)
		mockOutputRepo.On("GetByID", "output_001").Return(output, nil)
		mockPipeRepo.On("GetByInputID", "input_001").Return([]model.Pipe{existingPipe}, nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestPipeService_GetByID(t *testing.T) {
	t.Run("成功获取管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		expected := &model.Pipe{
			ID:       "pipe_001",
			InputID:  "input_001",
			OutputID: "output_001",
		}

		mockPipeRepo.On("GetByID", "pipe_001").Return(expected, nil)

		// Act
		result, err := svc.GetByID("pipe_001")

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, expected, result)
		mockPipeRepo.AssertExpectations(t)
	})

	t.Run("管道不存在", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		mockPipeRepo.On("GetByID", "nonexistent").Return(nil, assert.AnError)

		// Act
		result, err := svc.GetByID("nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockPipeRepo.AssertExpectations(t)
	})
}

func TestPipeService_Delete(t *testing.T) {
	t.Run("成功删除管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		existing := &model.Pipe{
			ID:       "pipe_001",
			InputID:  "input_001",
			OutputID: "output_001",
			Status:   model.PipeStatusStopped,
		}

		mockPipeRepo.On("GetByID", "pipe_001").Return(existing, nil)
		mockEngine.On("Disconnect", "input_001", "output_001").Return(nil)
		mockPipeRepo.On("Delete", "pipe_001").Return(nil)

		// Act
		err := svc.Delete("pipe_001")

		// Assert
		assert.NoError(t, err)
		mockPipeRepo.AssertExpectations(t)
		mockEngine.AssertExpectations(t)
	})

	t.Run("不能删除运行中的管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		existing := &model.Pipe{
			ID:     "pipe_001",
			Status: model.PipeStatusRunning,
		}

		mockPipeRepo.On("GetByID", "pipe_001").Return(existing, nil)

		// Act
		err := svc.Delete("pipe_001")

		// Assert
		assert.Error(t, err)
		mockPipeRepo.AssertNotCalled(t, "Delete")
	})
}

func TestPipeService_Start(t *testing.T) {
	t.Run("成功启动管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		existing := &model.Pipe{
			ID:       "pipe_001",
			InputID:  "input_001",
			OutputID: "output_001",
			Status:   model.PipeStatusStopped,
		}

		mockPipeRepo.On("GetByID", "pipe_001").Return(existing, nil)
		mockEngine.On("Connect", "input_001", "output_001").Return(nil)
		mockPipeRepo.On("UpdateStatus", "pipe_001", model.PipeStatusRunning).Return(nil)

		// Act
		err := svc.Start("pipe_001")

		// Assert
		assert.NoError(t, err)
		mockPipeRepo.AssertExpectations(t)
		mockEngine.AssertExpectations(t)
	})

	t.Run("不能启动已运行的管道", func(t *testing.T) {
		// Arrange
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		existing := &model.Pipe{
			ID:     "pipe_001",
			Status: model.PipeStatusRunning,
		}

		mockPipeRepo.On("GetByID", "pipe_001").Return(existing, nil)

		// Act
		err := svc.Start("pipe_001")

		// Assert
		assert.Error(t, err)
		mockPipeRepo.AssertNotCalled(t, "UpdateStatus")
	})
}
