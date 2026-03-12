package lbr

import "log"

var debugMode bool

// SetDebugMode 设置调试模式
func SetDebugMode(enabled bool) {
	debugMode = enabled
}

// debugLog 调试日志输出（仅在debug模式下）
func debugLog(format string, v ...interface{}) {
	if debugMode {
		log.Printf(format, v...)
	}
}
