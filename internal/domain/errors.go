package domain

import "errors"

var ErrInvalid = errors.New("请求无效")
var ErrConflict = errors.New("修订冲突")
var ErrState = errors.New("当前状态不允许此操作")
var ErrNotFound = errors.New("未找到个案")
var ErrSealed = errors.New("个案已封存")
var ErrIntegrity = errors.New("完整性校验失败")
var ErrPersistence = errors.New("持久化失败")

// PersistenceError 为存储失败补充稳定的操作分类，供传输层决定响应状态。
// CauseText 用于避免把平台相关的错误结构写入 JSON 响应。
type PersistenceError struct {
	Operation string
	CauseText string
}

func (e *PersistenceError) Error() string {
	return e.Operation + ": " + e.CauseText
}

func Persistence(operation string, cause error) error {
	return &PersistenceError{Operation: operation, CauseText: cause.Error()}
}

// DetailError 保留稳定错误类别，同时向 HTTP 层提供可操作的业务细节。
type DetailError struct {
	Kind    error
	Message string
	Details map[string]interface{}
}

func (e *DetailError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Kind.Error()
}

func (e *DetailError) Unwrap() error { return e.Kind }

func Invalid(message string, details map[string]interface{}) error {
	return &DetailError{Kind: ErrInvalid, Message: message, Details: details}
}

func Conflict(message string, details map[string]interface{}) error {
	return &DetailError{Kind: ErrConflict, Message: message, Details: details}
}
