// Package streamio 处理流式响应的网络刷新
package streamio

import (
	"io"
	"net/http"
)

// Flush 穿过响应包装器并返回底层网络刷新错误
func Flush(writer io.Writer) error {
	for {
		switch value := writer.(type) {
		case interface{ FlushError() error }:
			return value.FlushError()
		case interface{ Unwrap() http.ResponseWriter }:
			writer = value.Unwrap()
		case http.ResponseWriter:
			return http.NewResponseController(value).Flush()
		default:
			return nil
		}
	}
}
