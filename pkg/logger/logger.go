package logger

import (
	"fmt"
	"log"
	"os"
)

var (
	debugEnabled = os.Getenv("GROVE_DEBUG") == "true" || os.Getenv("DEBUG") == "true"
)

// Debug logs a debug message (only if debug mode is enabled)
func Debug(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// Info logs an informational message
func Info(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}

// Error logs an error message
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
}

// Warn logs a warning message
func Warn(format string, args ...interface{}) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}