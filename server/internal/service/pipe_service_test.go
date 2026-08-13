package service

import (
	"context"
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
		mockPipeRepo := new(MockPipeRepo)
		mockInputRepo := new(MockInputRepo)
		mockOutputRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

		pipe := &model.Pipe{
			ID:       "pipe_001",
			InputID:  "input_001",
			OutputID: "output_001",
			Status:   model.PipeStatusStopped,
		}
		input := &model.Input{ID: "input_001", Name: "输入", Type: "file", Config: `{"path":"/tmp/test.mp4"}`}
		output := &model.Output{ID: "output_001", Name: "输出", Type: "http-flv", Config: `{"addr":":8080"}`}

		mockPipeRepo.On("GetByID", "pipe_001").Return(pipe, nil)
		mockInputRepo.On("GetByID", "input_001").Return(input, nil)
		mockOutputRepo.On("GetByID", "output_001").Return(output, nil)
		mockEngine.On("CreateInput", mock.Anything).Return(nil, nil)
		mockEngine.On("CreateOutput", mock.Anything).Return(nil, nil)
		mockEngine.On("Connect", "input_001", "output_001").Return(nil)
		mockEngine.On("StartOutput", "output_001").Return(nil)
		mockEngine.On("StartInput", "input_001").Return(nil)
		mockEngine.On("StartPipe", "input_001", "output_001").Return(nil)
		mockPipeRepo.On("UpdateStatus", "pipe_001", model.PipeStatusRunning).Return(nil)
		mockInputRepo.On("UpdateStatus", "input_001", model.InputStatusRunning).Return(nil)
		mockOutputRepo.On("UpdateStatus", "output_001", model.OutputStatusRunning).Return(nil)

		err := svc.Start("pipe_001")

		assert.NoError(t, err)
		mockPipeRepo.AssertExpectations(t)
		mockInputRepo.AssertExpectations(t)
		mockOutputRepo.AssertExpectations(t)
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

// TestPipeService_Stop_StopsOrphanedIO 停止管道后，无其他运行管道引用的输入/输出应被停止并同步状态。
func TestPipeService_Stop_StopsOrphanedIO(t *testing.T) {
	mockPipeRepo := new(MockPipeRepo)
	mockInputRepo := new(MockInputRepo)
	mockOutputRepo := new(MockOutputRepo)
	mockEngine := new(MockMediaEngine)
	svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

	pipe := &model.Pipe{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusRunning}
	mockPipeRepo.On("GetByID", "pipe_001").Return(pipe, nil)
	mockEngine.On("Disconnect", "input_001", "output_001").Return(nil)
	mockPipeRepo.On("UpdateStatus", "pipe_001", model.PipeStatusStopped).Return(nil)
	mockPipeRepo.On("GetByInputID", "input_001").Return([]model.Pipe{{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusStopped}}, nil)
	mockPipeRepo.On("GetByOutputID", "output_001").Return([]model.Pipe{{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusStopped}}, nil)
	mockEngine.On("RemoveInput", "input_001").Return(nil)
	mockEngine.On("RemoveOutput", "output_001").Return(nil)
	mockInputRepo.On("UpdateStatus", "input_001", model.InputStatusStopped).Return(nil)
	mockOutputRepo.On("UpdateStatus", "output_001", model.OutputStatusStopped).Return(nil)

	err := svc.Stop("pipe_001")
	assert.NoError(t, err)
	mockEngine.AssertCalled(t, "RemoveInput", "input_001")
	mockEngine.AssertCalled(t, "RemoveOutput", "output_001")
	mockInputRepo.AssertCalled(t, "UpdateStatus", "input_001", model.InputStatusStopped)
	mockOutputRepo.AssertCalled(t, "UpdateStatus", "output_001", model.OutputStatusStopped)
}

// TestPipeService_Stop_KeepsSharedIO 输入/输出仍被其他运行管道引用时不应被停止。
func TestPipeService_Stop_KeepsSharedIO(t *testing.T) {
	mockPipeRepo := new(MockPipeRepo)
	mockInputRepo := new(MockInputRepo)
	mockOutputRepo := new(MockOutputRepo)
	mockEngine := new(MockMediaEngine)
	svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

	pipe := &model.Pipe{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusRunning}
	other := &model.Pipe{ID: "pipe_002", InputID: "input_001", OutputID: "output_002", Status: model.PipeStatusRunning}
	mockPipeRepo.On("GetByID", "pipe_001").Return(pipe, nil)
	mockEngine.On("Disconnect", "input_001", "output_001").Return(nil)
	mockPipeRepo.On("UpdateStatus", "pipe_001", model.PipeStatusStopped).Return(nil)
	// input 仍有另一运行管道；output_001 无其他运行管道 → 只停 output
	mockPipeRepo.On("GetByInputID", "input_001").Return([]model.Pipe{{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusStopped}, *other}, nil)
	mockPipeRepo.On("GetByOutputID", "output_001").Return([]model.Pipe{{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusStopped}}, nil)
	mockEngine.On("RemoveOutput", "output_001").Return(nil)
	mockOutputRepo.On("UpdateStatus", "output_001", model.OutputStatusStopped).Return(nil)

	err := svc.Stop("pipe_001")
	assert.NoError(t, err)
	mockEngine.AssertNotCalled(t, "RemoveInput", "input_001")
	mockEngine.AssertCalled(t, "RemoveOutput", "output_001")
}

// TestPipeService_Stop_AlreadyStopped 重复停止报错。
func TestPipeService_Stop_AlreadyStopped(t *testing.T) {
	mockPipeRepo := new(MockPipeRepo)
	mockInputRepo := new(MockInputRepo)
	mockOutputRepo := new(MockOutputRepo)
	mockEngine := new(MockMediaEngine)
	svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

	pipe := &model.Pipe{ID: "pipe_001", Status: model.PipeStatusStopped}
	mockPipeRepo.On("GetByID", "pipe_001").Return(pipe, nil)

	err := svc.Stop("pipe_001")
	assert.Error(t, err)
	mockEngine.AssertNotCalled(t, "Disconnect")
}

// TestInputService_Stop_CascadePipes 停止输入后其运行中的管道应置 stopped，输出级联停止。
func TestInputService_Stop_CascadePipes(t *testing.T) {
	mockInputRepo := new(MockInputRepo)
	mockPipeRepo := new(MockPipeRepo)
	mockOutputRepo := new(MockOutputRepo)
	mockEngine := new(MockMediaEngine)
	svc := NewInputService(mockInputRepo, mockEngine, mockPipeRepo, mockOutputRepo)

	input := &model.Input{ID: "input_001", Status: model.InputStatusRunning}
	pipe := &model.Pipe{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusRunning}
	mockInputRepo.On("GetByID", "input_001").Return(input, nil)
	mockEngine.On("RemoveInput", "input_001").Return(nil)
	mockInputRepo.On("UpdateStatus", "input_001", model.InputStatusStopped).Return(nil)
	mockPipeRepo.On("GetByInputID", "input_001").Return([]model.Pipe{*pipe}, nil)
	mockPipeRepo.On("UpdateStatus", "pipe_001", model.PipeStatusStopped).Return(nil)
	mockPipeRepo.On("GetByOutputID", "output_001").Return([]model.Pipe{{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusStopped}}, nil)
	mockEngine.On("RemoveOutput", "output_001").Return(nil)
	mockOutputRepo.On("UpdateStatus", "output_001", model.OutputStatusStopped).Return(nil)

	err := svc.Stop("input_001")
	assert.NoError(t, err)
	mockPipeRepo.AssertCalled(t, "UpdateStatus", "pipe_001", model.PipeStatusStopped)
	mockOutputRepo.AssertCalled(t, "UpdateStatus", "output_001", model.OutputStatusStopped)
}

// TestPipeService_Restore 恢复运行中的管道并同步状态。
func TestPipeService_Restore(t *testing.T) {
	mockPipeRepo := new(MockPipeRepo)
	mockInputRepo := new(MockInputRepo)
	mockOutputRepo := new(MockOutputRepo)
	mockEngine := new(MockMediaEngine)
	svc := NewPipeService(mockPipeRepo, mockInputRepo, mockOutputRepo, mockEngine)

	runningPipe := &model.Pipe{ID: "pipe_001", InputID: "input_001", OutputID: "output_001", Status: model.PipeStatusRunning}
	stoppedPipe := &model.Pipe{ID: "pipe_002", InputID: "input_002", OutputID: "output_002", Status: model.PipeStatusStopped}
	input := &model.Input{ID: "input_001", Name: "输入", Type: "file", Config: `{"path":"/tmp/t.mp4"}`}
	output := &model.Output{ID: "output_001", Name: "输出", Type: "http-flv", Config: `{"addr":":8080"}`}

	mockPipeRepo.On("GetAll").Return([]model.Pipe{*runningPipe, *stoppedPipe}, nil)
	mockInputRepo.On("GetByID", "input_001").Return(input, nil).Twice() // startPipe + 独立检查
	mockOutputRepo.On("GetByID", "output_001").Return(output, nil).Twice()
	mockEngine.On("CreateInput", mock.Anything).Return(nil, nil)
	mockEngine.On("CreateOutput", mock.Anything).Return(nil, nil)
	mockEngine.On("Connect", "input_001", "output_001").Return(nil)
	mockEngine.On("StartOutput", "output_001").Return(nil)
	mockEngine.On("StartInput", "input_001").Return(nil)
	mockEngine.On("StartPipe", "input_001", "output_001").Return(nil)
	mockInputRepo.On("UpdateStatus", "input_001", model.InputStatusRunning).Return(nil)
	mockOutputRepo.On("UpdateStatus", "output_001", model.OutputStatusRunning).Return(nil)
	// 独立 input/output 循环的 GetAll
	mockInputRepo.On("GetAll").Return([]model.Input{}, nil)
	mockOutputRepo.On("GetAll").Return([]model.Output{}, nil)

	err := svc.Restore(context.Background())
	assert.NoError(t, err)
	mockEngine.AssertCalled(t, "StartPipe", "input_001", "output_001")
	mockInputRepo.AssertCalled(t, "UpdateStatus", "input_001", model.InputStatusRunning)
}
