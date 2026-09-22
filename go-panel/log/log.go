package log

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

// Entry 面板日志条目（环形缓冲）
type Entry struct {
	Seq   int64  `json:"seq"`
	Time  string `json:"time"`
	Level string `json:"level"`
	Tag   string `json:"tag"`
	Msg   string `json:"msg"`
}

var (
	mu        sync.Mutex
	ring      []Entry
	seq       int64
	ringLimit = 4000

	appFile   *os.File
	debugFile *os.File

	consoleInfo  *log.Logger
	consoleError *log.Logger
	fileInfo     *log.Logger
	fileError    *log.Logger
	fileDebug    *log.Logger

	flags = log.Ldate | log.Ltime | log.Lmicroseconds

	InfoColor  = color.New(color.FgGreen).SprintFunc()
	ErrorColor = color.New(color.FgRed).SprintFunc()
	WarnColor  = color.New(color.FgYellow).SprintFunc()
)

var ansiColorRegex = regexp.MustCompile("\x1b\\[[0-9;]*m")

// removeAnsiColors 清除ANSI颜色代码（用于文件与面板日志）
func removeAnsiColors(args []interface{}) []interface{} {
	result := make([]interface{}, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case string:
			result[i] = ansiColorRegex.ReplaceAllString(v, "")
		default:
			result[i] = v
		}
	}
	return result
}

// Init 初始化文件日志目录（主程序最先调用一次）
func Init(dir string) {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	if app, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666); err == nil {
		appFile = app
		consoleInfo = log.New(os.Stdout, "", flags)
		consoleError = log.New(os.Stdout, "", flags)
		fileInfo = log.New(app, "", flags)
		fileError = log.New(app, "", flags)
	}
	if dbg, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666); err == nil {
		debugFile = dbg
		fileDebug = log.New(dbg, "", flags)
	}
}

// ensureLoggers 未调用 Init 时退化为纯控制台输出
func ensureLoggers() {
	if consoleInfo == nil {
		consoleInfo = log.New(os.Stdout, "", flags)
		consoleError = log.New(os.Stdout, "", flags)
		fileInfo = log.New(io.Discard, "", flags)
		fileError = log.New(io.Discard, "", flags)
		fileDebug = log.New(io.Discard, "", flags)
	}
}

// emit 统一输出：控制台（带色）+ 文件（去色）+ 面板环形缓冲（去色）
func emit(level, tag string, v ...interface{}) {
	msg := strings.TrimSuffix(fmt.Sprintln(v...), "\n")
	clean := strings.TrimSuffix(fmt.Sprintln(removeAnsiColors(v)...), "\n")
	ts := time.Now().Format("01-02 15:04:05")

	mu.Lock()
	defer mu.Unlock()
	ensureLoggers()

	switch level {
	case "ERROR":
		if tag != "" {
			consoleError.Printf("%s [%s] %s", ErrorColor("["+level+"]"), tag, msg)
		} else {
			consoleError.Printf("%s %s", ErrorColor("["+level+"]"), msg)
		}
		fileError.Printf("[%s] %s", level, clean)
	case "WARN":
		if tag != "" {
			consoleInfo.Printf("%s [%s] %s", WarnColor("["+level+"]"), tag, msg)
		} else {
			consoleInfo.Printf("%s %s", WarnColor("["+level+"]"), msg)
		}
		fileError.Printf("[%s] %s", level, clean)
	default:
		if tag != "" {
			consoleInfo.Printf("%s [%s] %s", InfoColor("["+level+"]"), tag, msg)
		} else {
			consoleInfo.Printf("%s %s", InfoColor("["+level+"]"), msg)
		}
		fileInfo.Printf("[%s] %s", level, clean)
	}

	seq++
	ring = append(ring, Entry{Seq: seq, Time: ts, Level: level, Tag: tag, Msg: clean})
	if len(ring) > ringLimit {
		ring = ring[len(ring)-ringLimit:]
	}
}

// Logger 带标签的日志器（每个账号/组件一个）
type Logger struct{ tag string }

// New 创建带标签的日志器
func New(tag string) *Logger { return &Logger{tag: tag} }

func (l *Logger) Info(v ...interface{})  { emit("INFO", l.tag, v...) }
func (l *Logger) Error(v ...interface{}) { emit("ERROR", l.tag, v...) }
func (l *Logger) Warn(v ...interface{})  { emit("WARN", l.tag, v...) }

// 全局无标签日志（client 包协议层调试等场景）
func Info(v ...interface{})  { emit("INFO", "", v...) }
func Error(v ...interface{}) { emit("ERROR", "", v...) }
func Warn(v ...interface{})  { emit("WARN", "", v...) }

// Debug 仅写入 debug.log 文件（包含完整请求/响应，量较大，不进面板）
func Debug(v ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoggers()
	fileDebug.Println(append([]interface{}{"[DEBUG]"}, removeAnsiColors(v)...)...)
}

// Entries 返回 seq 之后的日志与最新 seq
func Entries(since int64) (int64, []Entry) {
	mu.Lock()
	defer mu.Unlock()
	if len(ring) == 0 || since >= seq {
		return seq, nil
	}
	// 找到第一条 Seq > since 的位置
	idx := len(ring)
	for i, e := range ring {
		if e.Seq > since {
			idx = i
			break
		}
	}
	out := make([]Entry, len(ring)-idx)
	copy(out, ring[idx:])
	return seq, out
}

// Clear 清空面板日志缓冲（seq 保持单调，客户端按增量续传）
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	ring = nil
}
