package utils

import (
	"crypto/rand"
	"fmt"
	"time"
)

// GenerateID 生成唯一ID
func GenerateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// StringPtr 获取字符串指针
func StringPtr(s string) *string {
	return &s
}

// BoolPtr 获取布尔指针
func BoolPtr(b bool) *bool {
	return &b
}

// IntPtr 获取整数指针
func IntPtr(i int) *int {
	return &i
}

// Float64Ptr 获取浮点数指针
func Float64Ptr(f float64) *float64 {
	return &f
}

// PtrString 获取指针字符串值，如果指针为nil则返回默认值
func PtrString(p *string, defaultVal string) string {
	if p == nil {
		return defaultVal
	}
	return *p
}

// PtrBool 获取指针布尔值，如果指针为nil则返回默认值
func PtrBool(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}

// PtrInt 获取指针整数值，如果指针为nil则返回默认值
func PtrInt(p *int, defaultVal int) int {
	if p == nil {
		return defaultVal
	}
	return *p
}

// PtrFloat64 获取指针浮点数值，如果指针为nil则返回默认值
func PtrFloat64(p *float64, defaultVal float64) float64 {
	if p == nil {
		return defaultVal
	}
	return *p
}

// NowTimestamp 获取当前时间戳
func NowTimestamp() int64 {
	return time.Now().UnixMilli()
}
