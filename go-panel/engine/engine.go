// Package engine 常驻抢课引擎：每个账号一个 Engine（独立会话与状态机），
// Manager 负责多账号并发调度、账号 CRUD 与通知。
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"jin-grabber/client"
	"jin-grabber/config"
	"jin-grabber/log"
	"jin-grabber/pkg/course"
	"jin-grabber/pkg/login"
)

// 引擎状态
const (
	StateIdle          = "idle"           // 空闲
	StateWaiting       = "waiting"        // 等待定时
	StateGrabbing      = "grabbing"       // 定时抢课执行中
	StateWaitingCourse = "waiting_course" // 蹲课轮询中
	StateDone          = "done"           // 本轮结束
	StateStopped       = "stopped"        // 手动停止
	StateError         = "error"          // 出错
)

// CourseStatus 单课程运行状态
type CourseStatus struct {
	Name      string `json:"name"`
	Action    string `json:"action"` // 1选 0退
	State     string `json:"state"`  // pending|processing|success|failed|waiting
	Detail    string `json:"detail,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// cstLoc 教务系统时间均为北京时间
var cstLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// Engine 单账号抢课引擎
type Engine struct {
	mgr  *Manager
	id   string
	lg   *log.Logger
	mu   sync.Mutex
	busy bool

	ctxCancel context.CancelFunc

	state  string
	detail string
	errMsg string

	courses map[string]*CourseStatus // 课程名 -> 状态
	order   []string                 // 展示顺序
}

func newEngine(m *Manager, id string) *Engine {
	label := "账号" + id
	var cfgCopy *config.Config
	if a := m.st.Account(id); a != nil {
		label = a.Label
		cfgCopy = a.Config.Clone()
	}
	e := &Engine{
		mgr:     m,
		id:      id,
		lg:      log.New(label),
		state:   StateIdle,
		courses: map[string]*CourseStatus{},
	}
	if cfgCopy != nil {
		e.resetCourseStatus(cfgCopy)
	}
	return e
}

// ID 账号ID
func (e *Engine) ID() string { return e.id }

// SetLabel 账号改名后同步日志标签
func (e *Engine) SetLabel(label string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lg = log.New(label)
}

func (e *Engine) setState(state, detail string) {
	e.mu.Lock()
	e.state = state
	e.detail = detail
	e.mu.Unlock()
}

func (e *Engine) setError(msg string) {
	e.mu.Lock()
	e.state = StateError
	e.errMsg = msg
	e.detail = msg
	e.mu.Unlock()
	e.lg.Error("任务停止: ", msg)
}

func (e *Engine) setStopped() {
	e.mu.Lock()
	e.state = StateStopped
	e.detail = "已手动停止"
	e.mu.Unlock()
	e.lg.Info("任务已停止")
}

// resetCourseStatus 依据账号当前课程列表重建状态表
func (e *Engine) resetCourseStatus(cfg *config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.courses = map[string]*CourseStatus{}
	e.order = nil
	for _, k := range cfg.CourseKeys() {
		v, _ := cfg.Course.Get(k)
		action, _ := v.(string)
		e.courses[k] = &CourseStatus{Name: k, Action: action, State: "pending"}
		e.order = append(e.order, k)
	}
	e.errMsg = ""
}

func (e *Engine) setCourse(name, state, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs, ok := e.courses[name]
	if !ok {
		return
	}
	cs.State = state
	cs.Detail = detail
	cs.UpdatedAt = time.Now().Format("15:04:05")
}

// snapshot 引擎状态快照（供面板）
func (e *Engine) snapshot() (state, detail, errMsg string, busy bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state, e.detail, e.errMsg, e.busy
}

// courseViews 课程状态快照（按账号课程表顺序）
func (e *Engine) courseViews(cfg *config.Config) []CourseStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]CourseStatus, 0, len(e.order))
	for _, k := range e.order {
		if cs, ok := e.courses[k]; ok {
			out = append(out, *cs)
			continue
		}
		out = append(out, CourseStatus{Name: k, Action: cfg.CourseAction(k), State: "pending"})
	}
	return out
}

// Arm 启动（异步）：定时模式进入等待，蹲课模式立即开始轮询
func (e *Engine) Arm(trigger string) error {
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return errors.New("该账号任务已在运行")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.ctxCancel = cancel
	e.busy = true
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.busy = false
			e.mu.Unlock()
			cancel()
		}()
		e.run(ctx, trigger)
	}()
	return nil
}

// Stop 停止引擎
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.ctxCancel
	busy := e.busy
	e.mu.Unlock()
	if cancel != nil && busy {
		cancel()
	}
}

// Busy 是否运行中
func (e *Engine) Busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.busy
}

// LoginTest 测试登录（cookie 优先，账密兜底），成功则回写 cookie 到账号
func (e *Engine) LoginTest() error {
	if e.Busy() {
		return errors.New("该账号任务运行中，请先停止再测试")
	}
	acc := e.mgr.st.Account(e.id)
	if acc == nil {
		return errors.New("账号不存在")
	}
	cfg := acc.Config.Clone()
	c, err := login.Login(cfg, e.lg)
	if err != nil {
		_ = e.mgr.st.SetSessionState(e.id, "expired", "password")
		return err
	}
	// 用选课页校验会话真实性（失败仅提示，不影响登录结论）
	if err := c.GetClientBodyConfig(); err != nil {
		e.lg.Warn("会话校验提示: ", err)
	}
	_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")
	e.lg.Info("登录测试成功，最新 cookie 已保存（下次优先复用）")
	return nil
}

// RefreshCourseLib 在线重拉任务落实课程库并落盘
func (e *Engine) RefreshCourseLib() (int, error) {
	if e.Busy() {
		return 0, errors.New("该账号任务运行中，请先停止再更新")
	}
	acc := e.mgr.st.Account(e.id)
	if acc == nil {
		return 0, errors.New("账号不存在")
	}
	cfg := acc.Config.Clone()
	c, err := login.Login(cfg, e.lg)
	if err != nil {
		return 0, fmt.Errorf("登录失败: %w", err)
	}
	_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")
	lib, excel, err := course.GetCourseOnline(c, cfg, "")
	if err != nil {
		return 0, err
	}
	// 课程表按学年学期全校统一，整台 Docker 只保留一份（共享目录）
	dir := e.mgr.st.ResolveCourseDir(e.id)
	if err := course.SaveCourse(dir, lib); err != nil {
		return 0, err
	}
	if err := course.CourseRenameToExcel(excel, cfg.Time.XueNian+"-"+nextYear(cfg.Time.XueNian), cfg.Time.XueQi, dir); err != nil {
		e.lg.Warn("Excel 导出失败: ", err)
	}
	e.lg.Info("课程库已更新（共享），共 ", len(lib.Items), " 个教学班 → ", dir)
	return len(lib.Items), nil
}

// GrabOne 立即对单门课程执行一次选课（动作取课程表配置）
func (e *Engine) GrabOne(courseName string) error {
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return errors.New("该账号任务运行中")
	}
	e.busy = true
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.busy = false
			e.mu.Unlock()
		}()
		e.grabOne(courseName)
	}()
	return nil
}

func (e *Engine) grabOne(courseName string) {
	g := e.mgr.st.Global()
	acc := e.mgr.st.Account(e.id)
	if acc == nil {
		return
	}
	cfg := acc.Config.Clone()
	cfg.DryRun = bool01(g.DryRun)
	action := cfg.CourseAction(courseName)
	if action == "" {
		e.lg.Error("课程不在课程表中: ", courseName)
		return
	}
	e.setCourse(courseName, "processing", "手动抢课中")

	c, err := login.Login(cfg, e.lg)
	if err != nil {
		_ = e.mgr.st.SetSessionState(e.id, "expired", "password")
		e.setCourse(courseName, "failed", "登录失败: "+err.Error())
		return
	}
	_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")

	lib, err := course.GetCourse(c, cfg, e.lg, e.mgr.st.ResolveCourseDir(e.id))
	if err != nil {
		e.setCourse(courseName, "failed", err.Error())
		return
	}
	if err := e.ensureClientBody(c); err != nil {
		e.setCourse(courseName, "failed", err.Error())
		return
	}

	err = course.HandleCourse(c, cfg, e.lg, lib, courseName, action)
	if err != nil {
		e.setCourse(courseName, "failed", err.Error())
		e.mgr.notify(fmt.Sprintf("【金华科贸抢课】%s 手动抢课失败", acc.Label), courseName+" 失败: "+err.Error())
		return
	}
	e.setCourse(courseName, "success", "手动抢课完成")
	e.mgr.notify(fmt.Sprintf("【金华科贸抢课】%s 手动抢课完成", acc.Label), courseName+" 提交成功（干跑模式除外），请自行核对")
}

// ensureClientBody 优先读缓存，失败则在线获取并按需缓存
func (e *Engine) ensureClientBody(c *client.Client) error {
	if err := course.ReadClientBodyConfig(e.mgr.st.ClientBodyPath(e.id), c); err == nil {
		return nil
	}
	if err := c.GetClientBodyConfig(); err != nil {
		return fmt.Errorf("获取选课配置失败: %w", err)
	}
	e.lg.Info("选课配置获取成功")
	return nil
}

func nextYear(y string) string {
	n := 0
	for _, ch := range y {
		if ch < '0' || ch > '9' {
			return y
		}
		n = n*10 + int(ch-'0')
	}
	return fmt.Sprintf("%d", n+1)
}

func bool01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
