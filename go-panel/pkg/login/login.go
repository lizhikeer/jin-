package login

import (
	"errors"
	"strings"

	"jin-grabber/client"
	"jin-grabber/config"
	"jin-grabber/log"
	"jin-grabber/util"
)

// NewjwLogin 正方教务账号密码登录
func NewjwLogin(c *client.Client, cfg *config.Config, lg *log.Logger) error {
	lg.Info("访问登录页面获取表单与csrftoken...")
	csrftoken, hiddenFields, err := c.GetLoginInitialInfo()
	if err != nil {
		return err
	}

	lg.Info("获取RSA公钥...")
	publicKey, err := c.GetPublicKey()
	if err != nil {
		return err
	}

	lg.Info("RSA加密密码...")
	encryptedPassword, err := util.RsaEncrypt(publicKey.Modules, cfg.NewjwLogin.Password)
	if err != nil {
		return err
	}

	// 模拟预检三连（和Python版一致，失败不阻断）
	csrfTokenLogout := ""
	if hiddenFields != nil {
		csrfTokenLogout = hiddenFields["csrfTokenLogout"]
	}
	c.PreflightChecks(cfg.NewjwLogin.Username, csrfTokenLogout)

	lg.Info("正在提交登录表单...")
	loginReq := &client.LoginReq{
		Csrftoken:    csrftoken,
		Username:     cfg.NewjwLogin.Username,
		Password:     encryptedPassword,
		HiddenFields: hiddenFields,
	}
	result, err := c.NewjwLoginPost(loginReq)
	if err != nil {
		return err
	}

	// 判断是否明确包含错误信息
	if strings.Contains(result, "用户名或密码不正确") {
		return errors.New("用户名或密码不正确！")
	}
	if strings.Contains(result, "验证码") {
		return errors.New("系统触发验证码保护，请稍后或在浏览器端登录解除")
	}

	// 进一步检查登录态：访问主菜单验证
	if !c.CheckLogin() {
		// 如果页面包含学号，也可判定登录成功
		if !strings.Contains(result, cfg.NewjwLogin.Username) {
			return errors.New("正方登录失败，未能建立有效会话")
		}
	}

	lg.Info("✓ 正方教务登录成功")
	return nil
}

// Login 双重保险登录：优先复用已保存的cookies，失效则用正方账密重新登录；
// 成功后把新 cookies 回写到 cfg（由调用方持久化）。
func Login(cfg *config.Config, lg *log.Logger) (*client.Client, error) {
	c := client.NewClient(cfg)

	// 使用保存的cookies登录
	if cfg.Cookies.JSESSIONID != "" && cfg.Cookies.Enabled == "1" {
		lg.Info("正在使用保存的cookies登录...")
		err := c.LoadCookies(cfg)
		if err == nil {
			lg.Info("正在检查cookies是否有效...")
			if c.CheckLogin() {
				lg.Info("cookies会话有效！")
				return c, nil
			}
			err = c.GetClientBodyConfig()
			if err != nil && (err.Error() == "当前不属于选课阶段" || strings.Contains(err.Error(), "选课")) {
				lg.Info("cookies有效 (非选课阶段)")
				return c, nil
			}
			lg.Warn("cookies已失效或过期，准备重新登录...")
			c = client.NewClient(cfg)
		}
	}

	// 账密登录
	lg.Info("正在通过正方教务学号密码登录...")
	err := NewjwLogin(c, cfg, lg)
	if err != nil {
		lg.Error("正方教务登录失败: ", err)
		return nil, err
	}

	// 保存cookies
	err = c.SaveCookies(cfg)
	if err != nil {
		return nil, err
	}

	return c, nil
}
