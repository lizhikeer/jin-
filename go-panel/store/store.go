// Package store 负责 NAS 数据目录的持久化：
// global.json 全局设置、accounts.json 账号队列（含每个账号的课程表与会话 cookie）、
// courses/{id}.json 每账号课程库、clientbody/{id}.json 选课配置缓存。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jin-grabber/config"
	"github.com/iancoleman/orderedmap"
)

// SmtpEmail 全局邮件通知设置
type SmtpEmail struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	To       string `json:"to"`
	Enabled  bool   `json:"enabled"`
}

// GlobalSettings 全局运行设置
type GlobalSettings struct {
	Mode       string `json:"mode"`       // kill=定时抢课 wait=蹲课捡漏
	StartTime  string `json:"startTime"`  // 定时抢课开始时间 2006-01-02 15:04:05
	AutoStart  bool   `json:"autoStart"`  // 启动全部后 NAS 重启自动恢复挂机
	Interval   int    `json:"interval"`   // 蹲课轮询间隔（秒）
	DryRun     bool   `json:"dryRun"`     // 干跑：只查询不提交
	FallbackToWait bool `json:"fallbackToWait"` // 定时抢课失败自动转蹲课
	RefreshBeforeMin int `json:"refreshBeforeMin"` // 到点前N分钟刷新会话，0=关闭
	// WaitDropMode 蹲课模式下"退课条目"(动作=0)的执行策略：
	//   before   蹲到目标余量后，先执行退课条目，再提交选课（默认）
	//   conflict 先提交选课，返回冲突类错误时才退课并重试
	//   off      蹲课模式完全忽略退课条目
	WaitDropMode string `json:"waitDropMode"`
	SmtpEmail  SmtpEmail `json:"smtpEmail"`
}

// Account 账号队列中的一条账号
type Account struct {
	ID      string        `json:"id"`
	Label   string        `json:"label"`
	Enabled bool          `json:"enabled"`
	Config  config.Config `json:"config"`
	// 会话状态（登录后维护并持久化，实现 cookie+账密 双保险）
	SessionState     string `json:"sessionState"` // unknown|valid|expired
	SessionSource    string `json:"sessionSource"`// cookie|password
	SessionCheckedAt string `json:"sessionCheckedAt,omitempty"`
}

// Store 数据目录存储器
type Store struct {
	mu       sync.Mutex
	dir      string
	global   GlobalSettings
	accounts []*Account
}

// Load 从数据目录加载（文件不存在则创建默认值；
// 若存在旧版单账号 config.json 则自动导入为第一个账号）
func Load(dir string) (*Store, error) {
	s := &Store{dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "courses"), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "clientbody"), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.SharedCourseDir(), 0755); err != nil {
		return nil, err
	}

	// global.json
	if b, err := os.ReadFile(s.globalPath()); err == nil {
		if err := json.Unmarshal(b, &s.global); err != nil {
			return nil, fmt.Errorf("global.json 解析失败: %w", err)
		}
	} else {
		s.global = defaultGlobal()
		_ = s.persistGlobalLocked()
	}
	s.normalizeGlobal()

	// accounts.json
	b, err := os.ReadFile(s.accountsPath())
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.accounts); err != nil {
			return nil, fmt.Errorf("accounts.json 解析失败: %w", err)
		}
	case os.IsNotExist(err):
		s.accounts = nil
		// 兼容旧版：数据目录下存在 config.json 则自动导入
		if legacy, lerr := os.ReadFile(filepath.Join(dir, "config.json")); lerr == nil {
			acc, ierr := s.ImportLegacy(legacy, "导入账号")
			if ierr == nil {
				s.accounts = []*Account{acc}
				// 旧版同目录的 course.json 一并迁移为该账号课程库
				if cb, cerr := os.ReadFile(filepath.Join(dir, "course.json")); cerr == nil {
					_ = os.WriteFile(s.CoursePath(acc.ID), cb, 0644)
				}
			}
		}
		_ = s.persistAccountsLocked()
	default:
		return nil, err
	}

	return s, nil
}

func defaultGlobal() GlobalSettings {
	return GlobalSettings{
		Mode:             "kill",
		Interval:         60,
		FallbackToWait:   true,
		RefreshBeforeMin: 5,
		WaitDropMode:     "before",
	}
}

func (s *Store) normalizeGlobal() {
	if s.global.Mode != "kill" && s.global.Mode != "wait" {
		s.global.Mode = "kill"
	}
	if s.global.Interval <= 0 {
		s.global.Interval = 60
	}
	if s.global.Interval < 5 {
		s.global.Interval = 5
	}
	if s.global.RefreshBeforeMin < 0 {
		s.global.RefreshBeforeMin = 0
	}
	switch s.global.WaitDropMode {
	case "before", "conflict", "off":
	default:
		s.global.WaitDropMode = "before"
	}
}

func (s *Store) globalPath() string   { return filepath.Join(s.dir, "global.json") }
func (s *Store) accountsPath() string { return filepath.Join(s.dir, "accounts.json") }

// Dir 返回数据目录
func (s *Store) Dir() string { return s.dir }

// AccountWorkDir 账号工作目录（课程库/选课配置缓存所在）
func (s *Store) AccountWorkDir(id string) string { return filepath.Join(s.dir, "courses", id) }

// 共享课程库：整份"任务落实情况课程表"是按学年学期全校统一的，
// 因此一个 Docker 只保留一份，所有账号复用，避免每个账号都重新下载一遍。

// SharedCourseDir 共享课程库目录
func (s *Store) SharedCourseDir() string {
	return filepath.Join(s.dir, "courses", "_shared")
}

// SharedCoursePath 共享课程库文件（course.json）
func (s *Store) SharedCoursePath() string {
	return filepath.Join(s.SharedCourseDir(), "course.json")
}

// SharedCourseXlsx 共享课程库 Excel 导出名
func (s *Store) SharedCourseXlsx(xnmc, xqmc string) string {
	return filepath.Join(s.SharedCourseDir(), fmt.Sprintf("%s_%s_任务落实情况课程导出.xlsx", xnmc, xqmc))
}

// ResolveCourseDir 解析该账号实际使用的课程库目录：
// 账号目录里已有 course.json（历史数据/软链）则沿用，否则统一回落到共享目录。
// 这样新建账号不必再单独下载一份课程库；需要在线拉取时也写进共享目录。
func (s *Store) ResolveCourseDir(id string) string {
	if _, err := os.Stat(s.CoursePath(id)); err == nil {
		return s.AccountWorkDir(id)
	}
	return s.SharedCourseDir()
}

// ResolveCoursePath 解析该账号实际使用的课程库文件路径
func (s *Store) ResolveCoursePath(id string) string {
	return filepath.Join(s.ResolveCourseDir(id), "course.json")
}

// CoursePath 账号课程库文件路径
func (s *Store) CoursePath(id string) string {
	return filepath.Join(s.AccountWorkDir(id), "course.json")
}

// ClientBodyPath 账号选课配置缓存路径
func (s *Store) ClientBodyPath(id string) string {
	return filepath.Join(s.dir, "clientbody", id+".json")
}

func writeJSONAtomic(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) persistGlobalLocked() error {
	return writeJSONAtomic(s.globalPath(), s.global)
}

func (s *Store) persistAccountsLocked() error {
	return writeJSONAtomic(s.accountsPath(), s.accounts)
}

// Global 返回全局设置副本
func (s *Store) Global() GlobalSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.global
}

// SetGlobal 校验并保存全局设置
func (s *Store) SetGlobal(g GlobalSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Mode != "kill" && g.Mode != "wait" {
		return errors.New("mode 必须为 kill 或 wait")
	}
	if g.StartTime != "" {
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", g.StartTime, time.Local); err != nil {
			return errors.New("开始时间格式应为 2006-01-02 15:04:05")
		}
	}
	if g.Interval < 5 {
		g.Interval = 5
	}
	if g.SmtpEmail.Enabled && (g.SmtpEmail.Host == "" || g.SmtpEmail.Username == "" || g.SmtpEmail.Password == "" || g.SmtpEmail.To == "") {
		return errors.New("邮件通知已启用，请补全 SMTP 配置")
	}
	s.global = g
	s.normalizeGlobal()
	return s.persistGlobalLocked()
}

// Accounts 返回账号快照列表（浅拷贝，Course 指针共享，调用方只读）
func (s *Store) Accounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, *a)
	}
	return out
}

// Account 返回单个账号快照
func (s *Store) Account(id string) *Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.ID == id {
			cp := *a
			return &cp
		}
	}
	return nil
}

// AddAccount 新增账号（自动生成 ID）
func (s *Store) AddAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if acc.Label == "" {
		acc.Label = maskUser(acc.Config.NewjwLogin.Username)
	}
	acc.ID = newID()
	acc.Enabled = true
	if acc.Config.Cookies.Enabled == "" {
		acc.Config.Cookies.Enabled = "1"
	}
	if acc.Config.NewjwLogin.Level == "" {
		acc.Config.NewjwLogin.Level = "1"
	}
	s.accounts = append(s.accounts, acc)
	return s.persistAccountsLocked()
}

// UpdateAccount 用完整账号对象替换（按 ID 匹配）
func (s *Store) UpdateAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.accounts {
		if a.ID == acc.ID {
			acc.SessionState = a.SessionState
			acc.SessionSource = a.SessionSource
			acc.SessionCheckedAt = a.SessionCheckedAt
			cp := *acc
			s.accounts[i] = &cp
			return s.persistAccountsLocked()
		}
	}
	return errors.New("账号不存在")
}

// DeleteAccount 删除账号
func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.accounts {
		if a.ID == id {
			s.accounts = append(s.accounts[:i], s.accounts[i+1:]...)
			if err := s.persistAccountsLocked(); err != nil {
				return err
			}
			// 只清账号自己目录里的副本；共享课程库（course.json / _shared）不能删
			if p := s.AccountWorkDir(id); p != s.SharedCourseDir() {
				_ = os.Remove(filepath.Join(p, "course.json"))
			}
			_ = os.Remove(s.ClientBodyPath(id))
			return nil
		}
	}
	return errors.New("账号不存在")
}

// ReplaceCourses 整体替换账号课程列表（保持传入顺序）
func (s *Store) ReplaceCourses(id string, courses [][2]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.ID == id {
			om := orderedmap.New()
			for _, kv := range courses {
				om.Set(kv[0], kv[1])
			}
			a.Config.Course = om
			return s.persistAccountsLocked()
		}
	}
	return errors.New("账号不存在")
}

// SetCourseAction 更新单个课程动作（蹲选成功后置 0）
func (s *Store) SetCourseAction(id, courseName, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.ID == id && a.Config.Course != nil {
			a.Config.Course.Set(courseName, action)
			return s.persistAccountsLocked()
		}
	}
	return errors.New("账号或课程不存在")
}

// SetSession 更新会话状态与最新 cookie（双重保险的 cookie 半边）
func (s *Store) SetSession(id string, cookies config.Cookies, state, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.ID == id {
			if cookies.JSESSIONID != "" || cookies.Route != "" {
				a.Config.Cookies = cookies
			}
			a.SessionState = state
			a.SessionSource = source
			a.SessionCheckedAt = time.Now().Format("01-02 15:04")
			return s.persistAccountsLocked()
		}
	}
	return errors.New("账号不存在")
}

// SetSessionState 仅更新会话状态
func (s *Store) SetSessionState(id, state, source string) error {
	return s.SetSession(id, config.Cookies{}, state, source)
}

// ImportLegacy 导入旧版 config.json 为新账号
func (s *Store) ImportLegacy(b []byte, label string) (*Account, error) {
	var cfg config.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	acc := &Account{Label: label, Enabled: true, Config: cfg}
	if label == "" {
		acc.Label = maskUser(cfg.NewjwLogin.Username)
	}
	acc.ID = newID()
	return acc, nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "a" + hex.EncodeToString(b)
}

func maskUser(u string) string {
	u = strings.TrimSpace(u)
	if len(u) > 5 {
		return u[:3] + "****" + u[len(u)-2:]
	}
	if u == "" {
		u = "账号"
	}
	return u
}
