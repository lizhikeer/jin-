package config

import (
	"errors"

	"github.com/iancoleman/orderedmap"
)

// Config 单个账号的运行配置（文件读写由 store 层统一负责）
type Config struct {
	NewjwLogin              `json:"newjw_login"`
	Cookies                 `json:"cookies"`
	Time                    `json:"time"`
	Course                  *orderedmap.OrderedMap `json:"course"`
	WaitCourse              `json:"wait_course"`
	SmtpEmail               `json:"smtp_email"`
	StartTime               string `json:"start_time"`
	ClientBodyConfigEnabled string `json:"ClientBodyConfigEnabled,omitempty"`
	CrossGradeEnabled       string `json:"CrossGradeEnabled,omitempty"`
	// DryRun 运行期注入，不持久化：1=干跑（只查询与解析，不提交选/退课）
	DryRun string `json:"dryRun,omitempty"`
}

// NewjwLogin 正方教务登录配置
type NewjwLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Level    string `json:"level,omitempty"`
}

// Cookies Cookie 配置
type Cookies struct {
	JSESSIONID string `json:"JSESSIONID"`
	Route      string `json:"route"`
	Enabled    string `json:"enabled"`
}

// Time 学年学期配置
type Time struct {
	XueNian string `json:"XueNian"`
	XueQi   string `json:"XueQi"`
}

// WaitCourse 蹲课配置
type WaitCourse struct {
	Interval int    `json:"interval"`
	Enabled  string `json:"enabled"`
}

// SmtpEmail SMTP 邮件配置（旧版兼容字段，运行时通知使用全局设置）
type SmtpEmail struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	To       string `json:"to"`
	Enabled  string `json:"enabled"`
}

// CourseKeys 按顺序返回课程教学班名称
func (cfg *Config) CourseKeys() []string {
	if cfg.Course == nil {
		return nil
	}
	return cfg.Course.Keys()
}

// CourseAction 返回课程动作（1选 0退），不存在返回 ""
func (cfg *Config) CourseAction(name string) string {
	if cfg.Course == nil {
		return ""
	}
	v, ok := cfg.Course.Get(name)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Validate 校验配置（账号字段 + 学年学期 + 课程格式）
func (cfg *Config) Validate() error {
	if cfg.NewjwLogin.Username == "" || cfg.NewjwLogin.Password == "" {
		return errors.New("正方教务学号或密码为空")
	}
	if cfg.Time.XueNian == "" || cfg.Time.XueQi == "" {
		return errors.New("学年或学期为空")
	}
	if cfg.Course != nil {
		for _, k := range cfg.Course.Keys() {
			v, _ := cfg.Course.Get(k)
			if len(k) < 12 {
				return errors.New("课程信息格式错误: " + k)
			}
			if k[1:5] != cfg.Time.XueNian || k[11:12] != cfg.Time.XueQi {
				return errors.New("课程信息学年学期与配置不匹配: " + k)
			}
			if v == "" {
				return errors.New("课程信息值为空: " + k)
			}
		}
	}
	return nil
}

// Clone 深拷贝一份配置（Course 有序表单独复制）
func (cfg *Config) Clone() *Config {
	clone := *cfg
	if cfg.Course != nil {
		om := orderedmap.New()
		for _, k := range cfg.Course.Keys() {
			v, _ := cfg.Course.Get(k)
			om.Set(k, v)
		}
		clone.Course = om
	}
	return &clone
}
