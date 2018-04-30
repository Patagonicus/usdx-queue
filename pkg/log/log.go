package log

import (
	stdlog "log"
	"net/http"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger interface {
	Debug(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Panic(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Named(s string) Logger
	With(fields ...Field) Logger
	Sync() error
	NewStdLogAt(level Level) (*stdlog.Logger, error)
}

type Field = zapcore.Field

type Level = zapcore.Level

var (
	DebugLevel = zapcore.DebugLevel
	InfoLevel  = zapcore.InfoLevel
	WarnLevel  = zapcore.WarnLevel
	ErrorLevel = zapcore.ErrorLevel
	PanicLevel = zapcore.PanicLevel
	FatalLevel = zapcore.FatalLevel
)

var (
	NullLogger = fromZap(zap.NewNop(), nil)
)

// logger is a thin wrapper around *zap.Logger, that is only used to adapt it
// to out Logger interface.
type logger struct {
	l *zap.Logger
}

// make sure that logger implements Logger
var _ Logger = logger{}

// Tries to create a new logger suitable for debugging.
//
// It will log to the console in a human readable format and will log
// everything from Debug upwards (so, everything).
//
// If there is a problem creating the backing logger it will panic.
func NewDevelopment() Logger {
	config := zap.NewDevelopmentConfig()
	config.DisableStacktrace = true
	return fromZap(config.Build())
}

func NewProduction() Logger {
	return fromZap(zap.NewProduction())
}

func fromZap(z *zap.Logger, err error) Logger {
	if err != nil {
		panic(err)
	}
	return logger{z.WithOptions(zap.AddCallerSkip(1))}
}

func (l logger) Debug(msg string, fields ...Field) { l.l.Debug(msg, fields...) }
func (l logger) Error(msg string, fields ...Field) { l.l.Error(msg, fields...) }
func (l logger) Fatal(msg string, fields ...Field) { l.l.Fatal(msg, fields...) }
func (l logger) Info(msg string, fields ...Field)  { l.l.Info(msg, fields...) }
func (l logger) Panic(msg string, fields ...Field) { l.l.Panic(msg, fields...) }
func (l logger) Warn(msg string, fields ...Field)  { l.l.Warn(msg, fields...) }
func (l logger) Named(s string) Logger             { return logger{l.l.Named(s)} }
func (l logger) With(fields ...Field) Logger       { return logger{l.l.With(fields...)} }
func (l logger) Sync() error                       { return l.l.Sync() }

func (l logger) NewStdLogAt(level Level) (*stdlog.Logger, error) {
	return zap.NewStdLogAt(l.l, level)
}

var (
	Any         = zap.Any
	Binary      = zap.Binary
	Bool        = zap.Bool
	Bools       = zap.Bools
	ByteString  = zap.ByteString
	ByteStrings = zap.ByteStrings
	Complex64   = zap.Complex64
	Complex64s  = zap.Complex64s
	Complex128  = zap.Complex128
	Complex128s = zap.Complex128s
	Duration    = zap.Duration
	Durations   = zap.Durations
	Error       = zap.Error
	Errors      = zap.Errors
	Float32     = zap.Float32
	Float32s    = zap.Float32s
	Float64     = zap.Float64
	Float64s    = zap.Float64s
	Int         = zap.Int
	Ints        = zap.Ints
	Int8        = zap.Int8
	Int8s       = zap.Int8s
	Int16       = zap.Int16
	Int16s      = zap.Int16s
	Int32       = zap.Int32
	Int32s      = zap.Int32s
	Int64s      = zap.Int64s
	NamedError  = zap.NamedError
	Stack       = zap.Stack
	String      = zap.String
	Strings     = zap.Strings
	Stringer    = zap.Stringer
	Time        = zap.Time
	Times       = zap.Times
	Uint        = zap.Uint
	Uints       = zap.Uints
	Uint8       = zap.Uint8
	Uint8s      = zap.Uint8s
	Uint16      = zap.Uint16
	Uint16s     = zap.Uint16s
	Uint32      = zap.Uint32
	Uint32s     = zap.Uint32s
	Uint64      = zap.Uint64
	Uint64s     = zap.Uint64s
	Uintptr     = zap.Uintptr
	Uintptrs    = zap.Uintptrs
)

func Request(value *http.Request) Field {
	return NamedRequest("request", value)
}

func NamedRequest(key string, value *http.Request) Field {
	return zap.Object(key, zapcore.ObjectMarshalerFunc(func(e zapcore.ObjectEncoder) error {
		e.AddString("method", value.Method)
		e.AddString("url", value.URL.String())
		e.AddString("proto", value.Proto)
		e.AddString("host", value.Host)
		e.AddString("remote addr", value.RemoteAddr)
		return nil
	}))
}
