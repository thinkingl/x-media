package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/x-media/x-media-server/internal/media"
	"github.com/x-media/x-media-server/internal/model"
)

// MockInputRepo 模拟输入端仓储
type MockInputRepo struct {
	mock.Mock
}

func (m *MockInputRepo) Create(input *model.Input) error {
	args := m.Called(input)
	return args.Error(0)
}

func (m *MockInputRepo) GetByID(id string) (*model.Input, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Input), args.Error(1)
}

func (m *MockInputRepo) GetAll() ([]model.Input, error) {
	args := m.Called()
	return args.Get(0).([]model.Input), args.Error(1)
}

func (m *MockInputRepo) Update(input *model.Input) error {
	args := m.Called(input)
	return args.Error(0)
}

func (m *MockInputRepo) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockInputRepo) UpdateStatus(id string, status string) error {
	args := m.Called(id, status)
	return args.Error(0)
}

func (m *MockInputRepo) UpdateMediaInfo(id string, mediaInfo string) error {
	args := m.Called(id, mediaInfo)
	return args.Error(0)
}

// MockInputStream 模拟输入流
type MockInputStream struct {
	mock.Mock
}

func (m *MockInputStream) ID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockInputStream) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockInputStream) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockInputStream) Status() media.StreamStatus {
	args := m.Called()
	return args.Get(0).(media.StreamStatus)
}

func (m *MockInputStream) ReadPacket() (*media.MediaPacket, error) {
	args := m.Called()
	return args.Get(0).(*media.MediaPacket), args.Error(1)
}

func (m *MockInputStream) OnPacket(handler media.PacketHandler) {
	m.Called(handler)
}

// MockOutputStream 模拟输出流
type MockOutputStream struct {
	mock.Mock
}

func (m *MockOutputStream) ID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockOutputStream) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockOutputStream) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockOutputStream) Status() media.StreamStatus {
	args := m.Called()
	return args.Get(0).(media.StreamStatus)
}

func (m *MockOutputStream) WritePacket(pkt *media.MediaPacket) error {
	args := m.Called(pkt)
	return args.Error(0)
}

// MockMediaEngine 模拟媒体引擎
type MockMediaEngine struct {
	mock.Mock
}

func (m *MockMediaEngine) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockMediaEngine) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMediaEngine) CreateInput(config *media.InputConfig) (media.InputStream, error) {
	args := m.Called(config)
	return args.Get(0).(media.InputStream), args.Error(1)
}

func (m *MockMediaEngine) CreateOutput(config *media.OutputConfig) (media.OutputStream, error) {
	args := m.Called(config)
	return args.Get(0).(media.OutputStream), args.Error(1)
}

func (m *MockMediaEngine) Connect(inputID, outputID string) error {
	args := m.Called(inputID, outputID)
	return args.Error(0)
}

func (m *MockMediaEngine) Disconnect(inputID, outputID string) error {
	args := m.Called(inputID, outputID)
	return args.Error(0)
}

func (m *MockMediaEngine) RemoveInput(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockMediaEngine) RemoveOutput(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockMediaEngine) StartInput(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockMediaEngine) StartOutput(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockMediaEngine) StartOutputWithFile(id string, filePath string) error {
	args := m.Called(id, filePath)
	return args.Error(0)
}

func (m *MockMediaEngine) GetOutput(id string) (media.OutputStream, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(media.OutputStream), args.Error(1)
}

func TestInputService_Create(t *testing.T) {
	t.Run("成功创建MP4输入端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		req := &CreateInputRequest{
			Name:   "测试MP4",
			Type:   "file",
			Config: `{"path":"/data/test.mp4","loop":true}`,
		}

		mockRepo.On("Create", mock.AnythingOfType("*model.Input")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "测试MP4", result.Name)
		assert.Equal(t, "file", result.Type)
		assert.NotEmpty(t, result.ID)
		assert.Equal(t, model.InputStatusStopped, result.Status)
		mockRepo.AssertExpectations(t)
	})

	t.Run("成功创建RTSP输入端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		req := &CreateInputRequest{
			Name:   "测试RTSP",
			Type:   "rtsp",
			Config: `{"url":"rtsp://example.com/stream","transport":"tcp"}`,
		}

		mockRepo.On("Create", mock.AnythingOfType("*model.Input")).Return(nil)

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "rtsp", result.Type)
		mockRepo.AssertExpectations(t)
	})

	t.Run("参数验证失败-缺少名称", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		req := &CreateInputRequest{
			Type:   "file",
			Config: `{"path":"/data/test.mp4"}`,
		}

		// Act
		result, err := svc.Create(req)

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockRepo.AssertNotCalled(t, "Create")
	})

	t.Run("参数验证失败-不支持的类型", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		req := &CreateInputRequest{
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

func TestInputService_GetByID(t *testing.T) {
	t.Run("成功获取输入端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		expected := &model.Input{
			ID:   "input_001",
			Name: "测试MP4",
			Type: "file",
		}

		mockRepo.On("GetByID", "input_001").Return(expected, nil)

		// Act
		result, err := svc.GetByID("input_001")

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, expected, result)
		mockRepo.AssertExpectations(t)
	})

	t.Run("输入端不存在", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		mockRepo.On("GetByID", "nonexistent").Return(nil, assert.AnError)

		// Act
		result, err := svc.GetByID("nonexistent")

		// Assert
		assert.Error(t, err)
		assert.Nil(t, result)
		mockRepo.AssertExpectations(t)
	})
}

func TestInputService_Delete(t *testing.T) {
	t.Run("成功删除输入端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		existing := &model.Input{
			ID:     "input_001",
			Name:   "测试",
			Status: model.InputStatusStopped,
		}

		mockRepo.On("GetByID", "input_001").Return(existing, nil)
		mockEngine.On("RemoveInput", "input_001").Return(nil)
		mockRepo.On("Delete", "input_001").Return(nil)

		// Act
		err := svc.Delete("input_001")

		// Assert
		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockEngine.AssertExpectations(t)
	})

	t.Run("不能删除运行中的输入端", func(t *testing.T) {
		// Arrange
		mockRepo := new(MockInputRepo)
		mockEngine := new(MockMediaEngine)
		svc := NewInputService(mockRepo, mockEngine)

		existing := &model.Input{
			ID:     "input_001",
			Name:   "测试",
			Status: model.InputStatusRunning,
		}

		mockRepo.On("GetByID", "input_001").Return(existing, nil)

		// Act
		err := svc.Delete("input_001")

		// Assert
		assert.Error(t, err)
		mockRepo.AssertNotCalled(t, "Delete")
	})
}
