// Package logging builds the process logger: JSON lines, snake_case fields,
// ISO8601 `time`. See CONVENTIONS.md §3.
package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Stack traces are not attached automatically: an access-log line at error
// level must stay one short line. Recover logs the stack of real panics.
//
// New returns a JSON logger at level (debug|info|warn|error, default info)
// and installs it as zap's global logger.
func New(level string) *zap.Logger {
	enc := zap.NewProductionEncoderConfig()
	enc.TimeKey = "time"
	enc.EncodeTime = zapcore.ISO8601TimeEncoder
	enc.EncodeDuration = zapcore.MillisDurationEncoder
	core := zapcore.NewCore(zapcore.NewJSONEncoder(enc), zapcore.Lock(os.Stderr), zap.NewAtomicLevelAt(ParseLevel(level)))
	l := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.DPanicLevel)).With(zap.String("service", "tide"))
	zap.ReplaceGlobals(l)
	return l
}

func ParseLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
