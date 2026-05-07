package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/x-media/x-media-server/internal/model"
)

// MockOutputRepo 模拟输出端仓储
type MockOutputRepo struct {
	mock.Mock
}

func (m *MockOutputRepo) Create(output *model.Output) error {
	args := m.Called(output)
	return args.Error(0)
}

func (m *MockOutputRepo) GetByID(id string) (*model.Output, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Output), args.Error(1)
}

func (m *MockOutputRepo) GetAll() ([]model.Output, error) {
	args := m.Called()
	return args.Get(0).([]model.Output), args.Error(1)
}

func (m *MockOutputRepo) Update(output *model.Output) error {
	args := m.Called(output)
	return args.Error(0)
}

func (m *MockOutputRepo) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockOutputRepo) UpdateStatus(id string, status string) error {
	args := m.Called(id, status)
	return args.Error(0)
}

func TestOutputService_Create(t *testing.T) {
	t.Run("成功创建RTMP输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		req := &CreateOutputRequest{
			Name:   "测试RTMP",
			Type:   "rtmp",
			Config: `{"url":"rtmp://live.example.com/live/test"}`,
		}

		mockRepo.On("Create", mock.AnythingOfType("*model.Output")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "测试RTMP", result.Name)
		assert.Equal(t, "rtmp", result.Type)
		assert.NotEmpty(t, result.ID)
		assert.Equal(t, model.OutputStatusStopped, result.Status)
		mockRepo.AssertExpectations(t)
	})

	t.Run("成功创建RTSP输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		req := &CreateOutputRequest{
			Name:   "测试RTSP",
			Type:   "rtsp",
			Config: `{"mode":"server","addr":":5544"}`,
		}

		mockRepo.On("Create", mock.AnythingOfType("*model.Output")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "rtsp", result.Type)
		mockRepo.AssertExpectations(t)
	})

	t.Run("成功创建HTTP-FLV输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		req := &CreateOutputRequest{
			Name:   "测试HTTP-FLV",
			Type:   "http-flv",
			Config: `{"addr":":8080"}`,
		}

		mockRepo.On("Create", mock.AnythingOfType("*model.Output")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "http-flv", result.Type)
		mockRepo.AssertExpectations(t)
	})

	t.Run("参数验证失败-不支持的类型", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		req := &CreateOutputRequest{
			Name:   "测试",
			Type:   "unsupported",
			Config: `{}`,
		}

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockRepo.AssertNotCalled(t, "Create")
	})
}

func TestOutputService_GetByID(t *testing.T) {
	t.Run("成功获取输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		expected := &model.Output{
			ID:   "output_001",
			Name: "测试RTMP",
			Type: "rtmp",
		}

		mockRepo.On("GetByID", "output_001").Return(expected, nil)

		// Act
		result, err := svc.GetByID("output_001")

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, expected, result)
		mockRepo.AssertExpectations(t)
	})

	t.Run("输出端不存在", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		mockRepo.On("GetByID", "nonexistent").Return(nil, assert.AnError)

		// Act
		result, err := svc.GetByID("nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockRepo.AssertExpectations(t)
	})
}

func TestOutputService_Delete(t *testing.T) {
	t.Run("成功删除输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		existing := &model.Output{
			ID:     "output_001",
			Name:   "测试",
			Status: model.OutputStatusStopped,
		}

		mockRepo.On("GetByID", "output_001").Return(existing, nil)
		mockEngine.On("RemoveOutput", "output_001").Return(nil)
		mockRepo.On("Delete", "output_001").Return(nil)

		// Act
		err := svc.Delete("output_001")

		// Assert
		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockEngine.AssertExpectations(t)
	})

	t.Run("不能删除运行中的输出端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockOutputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewOutputService(mockRepo, mockEngine)

		existing := &model.Output{
			ID:     "output_001",
			Name:   "测试",
			Status: model.OutputStatusRunning,
		}

		mockRepo.On("GetByID", "output_001").Return(existing, nil)

		// Act
		err := svc.Delete("output_001")

		// Assert
		assert.Error(t, err)
		mockRepo.AssertNotCalled(t, "Delete")
	})
}
