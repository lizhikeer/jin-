package engine

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"jin-grabber/config"
	"jin-grabber/log"
	"jin-grabber/store"
	"jin-grabber/util"
)

// Manager 多账号并发管理器
type Manager struct {
	st      *store.Store
	lg      *log.Logger
	mu      sync.Mutex
	engines map[string]*Engine
}

// NewManager 创建管理器并同步引擎；AutoStart 开启时（NAS 重启）自动恢复挂机
func NewManager(st *store.Store) *Manager {
	m := &Manager{st: st, lg: log.New("管理器"), engines: map[string]*Engine{}}
	m.syncEnginesLocked()
	if st.Global().AutoStart {
		go func() {
			time.Sleep(2 * time.Second)
			m.lg.Info("检测到自动启动开关已开启，恢复挂机任务...")
			m.StartAll("NAS重启自动恢复")
		}()
	}
	return m
}

func (m *Manager) syncEnginesLocked() {
	accs := m.st.Accounts()
	have := map[string]bool{}
	for _, a := range accs {
		have[a.ID] = true
		if _, ok := m.engines[a.ID]; !ok {
			m.engines[a.ID] = newEngine(m, a.ID)
		}
	}
	for id := range m.engines {
		if !have[id] {
			delete(m.engines, id)
		}
	}
}

func (m *Manager) engineFor(id string) (*Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.engines[id]
	if !ok {
		return nil, errors.New("账号不存在")
	}
	return e, nil
}

// EngineFor 获取账号引擎（webui 使用）
func (m *Manager) EngineFor(id string) (*Engine, error) {
	return m.engineFor(id)
}

// Store 暴露存储器（webui 读取设置/账号）
func (m *Manager) Store() *store.Store { return m.st }

// ---- 账号 CRUD ----

// AddAccountRequest 新增账号参数
type AddAccountRequest struct {
	Label         string `json:"label"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	NewjwUsername string `json:"newjwUsername"`
	NewjwPassword string `json:"newjwPassword"`
	CasUsername   string `json:"casUsername"`
	CasPassword   string `json:"casPassword"`
}

// AddAccount 新增账号
func (m *Manager) AddAccount(req AddAccountRequest) (*store.Account, error) {
	u := req.Username
	if u == "" {
		u = req.NewjwUsername
	}
	if u == "" {
		u = req.CasUsername
	}
	p := req.Password
	if p == "" {
		p = req.NewjwPassword
	}
	if p == "" {
		p = req.CasPassword
	}
	if u == "" || p == "" {
		return nil, errors.New("请填写学号和密码")
	}

	acc := &store.Account{Enabled: true, Config: config.Config{
		NewjwLogin: config.NewjwLogin{Username: u, Password: p, Level: "1"},
		Cookies:    config.Cookies{Enabled: "1"},
		Time:       defaultTerm(),
	}}
	if req.Label != "" {
		acc.Label = req.Label
	} else {
		acc.Label = u
	}
	if err := m.st.AddAccount(acc); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.syncEnginesLocked()
	m.mu.Unlock()
	m.lg.Info("新增账号: ", acc.Label)
	return acc, nil
}

// UpdateAccount 更新账号（编辑器整表提交）
func (m *Manager) UpdateAccount(acc *store.Account) error {
	if _, err := m.engineFor(acc.ID); err != nil {
		return err
	}
	if err := acc.Config.Validate(); err != nil {
		return err
	}
	if err := m.st.UpdateAccount(acc); err != nil {
		return err
	}
	if e, err := m.engineFor(acc.ID); err == nil {
		e.SetLabel(acc.Label)
	}
	m.mu.Lock()
	m.syncEnginesLocked()
	m.mu.Unlock()
	m.lg.Info("账号配置已更新: ", acc.Label)
	return nil
}

// DeleteAccount 删除账号（运行中禁止）
func (m *Manager) DeleteAccount(id string) error {
	e, err := m.engineFor(id)
	if err != nil {
		return err
	}
	if e.Busy() {
		return errors.New("账号任务运行中，请先停止")
	}
	if err := m.st.DeleteAccount(id); err != nil {
		return err
	}
	m.mu.Lock()
	m.syncEnginesLocked()
	m.mu.Unlock()
	m.lg.Info("账号已删除: ", id)
	return nil
}

// ImportLegacy 导入旧版 config.json
func (m *Manager) ImportLegacy(b []byte, label string) (*store.Account, error) {
	acc, err := m.st.ImportLegacy(b, label)
	if err != nil {
		return nil, err
	}
	if err := m.st.AddAccount(acc); err != nil {
		return nil, err
	}
	// 若数据目录存在旧版 course.json 且该账号还没有课程库，则迁移
	workDir := m.st.AccountWorkDir(acc.ID)
	if _, err := os.Stat(workDir + "/course.json"); os.IsNotExist(err) {
		if cb, cerr := os.ReadFile(m.st.Dir() + "/course.json"); cerr == nil {
			_ = os.WriteFile(workDir+"/course.json", cb, 0644)
		}
	}
	m.mu.Lock()
	m.syncEnginesLocked()
	m.mu.Unlock()
	m.lg.Info("已导入旧版配置为新账号: ", acc.Label)
	return acc, nil
}

// ---- 启停 ----

// StartAccount 启动单个账号
func (m *Manager) StartAccount(id string) error {
	e, err := m.engineFor(id)
	if err != nil {
		return err
	}
	return e.Arm("手动启动")
}

// StopAccount 停止单个账号
func (m *Manager) StopAccount(id string) error {
	e, err := m.engineFor(id)
	if err != nil {
		return err
	}
	e.Stop()
	return nil
}

// StartAll 启动全部启用账号（登录错峰 800ms/个），并打开自动恢复开关
func (m *Manager) StartAll(trigger string) {
	g := m.st.Global()
	g.AutoStart = true
	if err := m.st.SetGlobal(g); err != nil {
		m.lg.Error("保存自动启动开关失败: ", err)
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.engines))
	for _, a := range m.st.Accounts() {
		if a.Enabled {
			ids = append(ids, a.ID)
		}
	}
	m.mu.Unlock()
	m.lg.Info("启动全部账号任务，共 ", len(ids), " 个")
	for _, id := range ids {
		if e, err := m.engineFor(id); err == nil {
			if err := e.Arm(trigger); err != nil {
				m.lg.Warn("账号 ", id, " 启动失败: ", err)
			}
		}
		time.Sleep(800 * time.Millisecond)
	}
}

// StopAll 停止全部并关闭自动恢复开关
func (m *Manager) StopAll() {
	g := m.st.Global()
	g.AutoStart = false
	if err := m.st.SetGlobal(g); err != nil {
		m.lg.Error("保存自动启动开关失败: ", err)
	}
	m.mu.Lock()
	engines := make([]*Engine, 0, len(m.engines))
	for _, e := range m.engines {
		engines = append(engines, e)
	}
	m.mu.Unlock()
	for _, e := range engines {
		e.Stop()
	}
	m.lg.Info("已停止全部账号任务")
}

// ---- 单账号操作转发 ----

// LoginTest 测试登录
func (m *Manager) LoginTest(id string) error {
	e, err := m.engineFor(id)
	if err != nil {
		return err
	}
	return e.LoginTest()
}

// RefreshCourseLib 更新课程库
func (m *Manager) RefreshCourseLib(id string) (int, error) {
	e, err := m.engineFor(id)
	if err != nil {
		return 0, err
	}
	return e.RefreshCourseLib()
}

// GrabOne 立即抢单课
func (m *Manager) GrabOne(id, courseName string) error {
	e, err := m.engineFor(id)
	if err != nil {
		return err
	}
	return e.GrabOne(courseName)
}

// ---- 通知 ----

// notify 全局邮件通知（异步，失败仅记日志）
func (m *Manager) notify(subject, body string) {
	g := m.st.Global()
	if !g.SmtpEmail.Enabled {
		return
	}
	go func() {
		err := util.SendEmail(g.SmtpEmail.Host, g.SmtpEmail.Username, g.SmtpEmail.Password, g.SmtpEmail.To, subject, body)
		if err != nil {
			m.lg.Error("发送邮件失败: ", err)
		} else {
			m.lg.Info("通知邮件已发送: ", subject)
		}
	}()
}

// SendTestEmail 发送测试邮件
func (m *Manager) SendTestEmail() error {
	g := m.st.Global()
	if !g.SmtpEmail.Enabled {
		return errors.New("邮件通知未启用")
	}
	err := util.SendEmail(g.SmtpEmail.Host, g.SmtpEmail.Username, g.SmtpEmail.Password, g.SmtpEmail.To, "【金华科贸抢课】测试邮件", "这是一封测试邮件，收到即说明通知配置正确。")
	if err != nil {
		return err
	}
	m.lg.Info("测试邮件已发送")
	return nil
}

// ---- 面板视图 ----

// AccountView 账号面板视图（不含密码）
type AccountView struct {
	ID               string         `json:"id"`
	Label            string         `json:"label"`
	Enabled          bool           `json:"enabled"`
	CASUser          string         `json:"casUser"`
	SessionState     string         `json:"sessionState"`
	SessionSource    string         `json:"sessionSource"`
	SessionCheckedAt string         `json:"sessionCheckedAt"`
	CourseCount      int            `json:"courseCount"`
	State            string         `json:"state"`
	Detail           string         `json:"detail"`
	ErrMsg           string         `json:"errMsg"`
	Busy             bool           `json:"busy"`
	Courses          []CourseStatus `json:"courses"`
	Success          int            `json:"success"`
	Failed           int            `json:"failed"`
	Pending          int            `json:"pending"`
}

// Overview 面板总览
type Overview struct {
	ServerTime string          `json:"serverTime"`
	Global     store.GlobalSettings `json:"global"`
	Accounts   []AccountView   `json:"accounts"`
}

// Overview 生成面板总览快照
func (m *Manager) Overview() Overview {
	accs := m.st.Accounts()
	ov := Overview{
		ServerTime: time.Now().Format("2006-01-02 15:04:05"),
		Global:     m.st.Global(),
		Accounts:   make([]AccountView, 0, len(accs)),
	}
	for _, a := range accs {
		e := m.engines[a.ID]
		v := AccountView{
			ID:               a.ID,
			Label:            a.Label,
			Enabled:          a.Enabled,
			CASUser:          maskUser(a.Config.NewjwLogin.Username),
			SessionState:     a.SessionState,
			SessionSource:    a.SessionSource,
			SessionCheckedAt: a.SessionCheckedAt,
			CourseCount:      len(a.Config.CourseKeys()),
		}
		if e != nil {
			state, detail, errMsg, busy := e.snapshot()
			v.State, v.Detail, v.ErrMsg, v.Busy = state, detail, errMsg, busy
			v.Courses = e.courseViews(&a.Config)
			for _, cs := range v.Courses {
				switch cs.State {
				case "success":
					v.Success++
				case "failed":
					v.Failed++
				default:
					v.Pending++
				}
			}
		}
		ov.Accounts = append(ov.Accounts, v)
	}
	return ov
}

// ---- 小工具 ----

func defaultTerm() config.Time {
	now := time.Now()
	y := now.Year()
	var xn, xq string
	switch {
	case now.Month() >= 6:
		xn, xq = fmt.Sprint(y), "1"
	case now.Month() <= 2:
		xn, xq = fmt.Sprint(y-1), "1"
	default:
		xn, xq = fmt.Sprint(y-1), "2"
	}
	return config.Time{XueNian: xn, XueQi: xq}
}

func maskUser(u string) string {
	if len(u) > 5 {
		return u[:3] + "****" + u[len(u)-2:]
	}
	return u
}
