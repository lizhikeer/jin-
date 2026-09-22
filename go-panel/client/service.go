package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
	"jin-grabber/vars"
)

func isSessionExpired(result []byte) bool {
	s := string(result)
	return strings.Contains(s, "login_slogin.html") ||
		strings.Contains(s, "统一身份认证") ||
		strings.Contains(s, "未登录") ||
		strings.Contains(s, "请重新登录")
}

// GetLoginInitialInfo 访问登录页获取 csrftoken 以及所有隐藏表单字段
func (c *Client) GetLoginInitialInfo() (string, map[string]string, error) {
	loginURL := fmt.Sprintf("%s/xtgl/login_slogin.html", c.BaseURL)
	result, _, err := c.Get(loginURL, nil)
	if err != nil {
		return "", nil, err
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(result)))
	if err != nil {
		return "", nil, err
	}

	hiddenFields := make(map[string]string)
	nodes := htmlquery.Find(doc, `//input[@type="hidden"]`)
	for _, n := range nodes {
		nameNode := htmlquery.FindOne(n, `@name`)
		valNode := htmlquery.FindOne(n, `@value`)
		if nameNode != nil {
			k := htmlquery.InnerText(nameNode)
			v := ""
			if valNode != nil {
				v = htmlquery.InnerText(valNode)
			}
			hiddenFields[k] = v
		}
	}

	csrftoken := hiddenFields["csrftoken"]
	if csrftoken == "" {
		tokenNode := htmlquery.FindOne(doc, `//input[@name="csrftoken"]/@value`)
		if tokenNode != nil {
			csrftoken = htmlquery.InnerText(tokenNode)
			hiddenFields["csrftoken"] = csrftoken
		}
	}

	if csrftoken == "" {
		return "", nil, errors.New("获取csrftoken失败")
	}

	return csrftoken, hiddenFields, nil
}

// GetCsrftoken 获取csrftoken
func (c *Client) GetCsrftoken() (string, error) {
	token, _, err := c.GetLoginInitialInfo()
	return token, err
}

// PreflightChecks 模拟登录前的浏览器预检请求（忽略返回错误）
func (c *Client) PreflightChecks(username, csrfTokenLogout string) {
	headers := map[string]string{
		"X-Requested-With": "XMLHttpRequest",
		"Referer":          fmt.Sprintf("%s/xtgl/login_slogin.html", c.BaseURL),
	}
	_, _, _ = c.Post(fmt.Sprintf("%s/xtgl/yhgl_cxXxqrCheck.html", c.BaseURL), fmt.Sprintf("yhm=%s", username), headers)
	_, _, _ = c.Post(fmt.Sprintf("%s/xtgl/login_cxDlxgxx.html", c.BaseURL), fmt.Sprintf("yhm=%s", username), headers)
	if csrfTokenLogout != "" {
		_, _, _ = c.Post(fmt.Sprintf("%s/xtgl/login_logoutAccount.html", c.BaseURL), fmt.Sprintf("csrfTokenLogout=%s", csrfTokenLogout), headers)
	}
}

// GetPublicKey 获取RSA公钥
func (c *Client) GetPublicKey() (*GetPublicKeyResp, error) {
	u := fmt.Sprintf("%s/xtgl/login_getPublicKey.html?time=%d", c.BaseURL, time.Now().UnixMilli())
	result, _, err := c.Get(u, map[string]string{
		"Referer":          fmt.Sprintf("%s/xtgl/login_slogin.html", c.BaseURL),
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return nil, err
	}
	var PublicKeyResp GetPublicKeyResp
	err = json.Unmarshal(result, &PublicKeyResp)
	if err != nil {
		return nil, err
	}

	return &PublicKeyResp, nil
}

// NewjwLoginPost 正方教务登录请求
func (c *Client) NewjwLoginPost(req *LoginReq) (string, error) {
	loginURL := fmt.Sprintf("%s/xtgl/login_slogin.html?time=%d", c.BaseURL, time.Now().UnixMilli())
	formData := req.ToFormData()
	headers := map[string]string{
		"Referer": fmt.Sprintf("%s/xtgl/login_slogin.html", c.BaseURL),
	}
	result, _, err := c.Post(loginURL, formData.Encode(), headers)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// CheckLogin 验证登录是否有效（请求首页菜单）
func (c *Client) CheckLogin() bool {
	menuURL := fmt.Sprintf("%s/xtgl/index_initMenu.html", c.BaseURL)
	result, code, err := c.Get(menuURL, nil)
	if err != nil || code != 200 {
		return false
	}
	s := string(result)
	if strings.Contains(s, "login_slogin.html") || strings.Contains(s, "用户名或密码不正确") {
		return false
	}
	return true
}

// GetCourse 获取课程（任务落实）
func (c *Client) GetCourse(req *GetCourseReq) (*GetCourseResp, *GetCourseToExcelResp, error) {
	courseURL := fmt.Sprintf("%s/rwlscx/rwlscx_cxRwlsIndex.html?doType=query&gnmkdm=%s", c.BaseURL, vars.DefaultGnmkdmTask)
	formData := req.ToFormData()
	result, _, err := c.Post(courseURL, formData.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	if isSessionExpired(result) {
		return nil, nil, errors.New("可能登录过期")
	}
	if strings.Contains(string(result), "无功能权限") || strings.Contains(string(result), "未开放") {
		return nil, nil, errors.New("任务落实查询并未开放")
	}

	var courseResp GetCourseResp
	err = json.Unmarshal(result, &courseResp)
	if err != nil {
		return nil, nil, err
	}
	var courseToExcelResp GetCourseToExcelResp
	err = json.Unmarshal(result, &courseToExcelResp)
	if err != nil {
		return nil, nil, err
	}

	return &courseResp, &courseToExcelResp, nil
}

// GetClientBodyConfig 获取选课配置
func (c *Client) GetClientBodyConfig() error {
	AnalysisConfig := func(doc *html.Node) error {
		AnalysisXkkzId := func(node []*html.Node) error {
			pattern := regexp.MustCompile(`queryCourse\(this,'(\d+)'`)
			pattern1 := regexp.MustCompile(`queryCourse\(this,'(?:[^']*)','(\w+)'`)
			for _, n := range node {
				match := pattern.FindStringSubmatch(htmlquery.InnerText(n))
				match1 := pattern1.FindStringSubmatch(htmlquery.InnerText(n))
				if match == nil || match1 == nil {
					return errors.New("XkkzId解析失败")
				}
				c.ClientBodyConfig.XkkzId[match[1]] = match1[1]
			}
			return nil
		}

		c.ClientBodyConfig = &ClientBodyConfig{
			XkkzId: make(map[string]string),
		}

		if node := htmlquery.FindOne(doc, `//input[@name="ccdm"]/@value`); node != nil {
			c.ClientBodyConfig.Ccdm = htmlquery.InnerText(node)
		} else {
			return errors.New("当前不属于选课阶段")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="bh_id"]/@value`); node != nil {
			c.ClientBodyConfig.BhId = htmlquery.InnerText(node)
		} else {
			return errors.New("bh_id获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="jg_id_1"]/@value`); node != nil {
			c.ClientBodyConfig.JgId = htmlquery.InnerText(node)
		} else {
			return errors.New("jg_id获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="xsbj"]/@value`); node != nil {
			c.ClientBodyConfig.Xsbj = htmlquery.InnerText(node)
		} else {
			return errors.New("xsbj获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="xz"]/@value`); node != nil {
			c.ClientBodyConfig.Xz = htmlquery.InnerText(node)
		} else {
			return errors.New("xz获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="mzm"]/@value`); node != nil {
			c.ClientBodyConfig.Mzm = htmlquery.InnerText(node)
		} else {
			return errors.New("mzm获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="xslbdm"]/@value`); node != nil {
			c.ClientBodyConfig.Xslbdm = htmlquery.InnerText(node)
		} else {
			return errors.New("xslbdm获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="xbm"]/@value`); node != nil {
			c.ClientBodyConfig.Xbm = htmlquery.InnerText(node)
		} else {
			return errors.New("xbm获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="zyfx_id"]/@value`); node != nil {
			c.ClientBodyConfig.ZyfxId = htmlquery.InnerText(node)
		} else {
			return errors.New("zyfx_id获取失败")
		}
		if node := htmlquery.FindOne(doc, `//input[@name="xqh_id"]/@value`); node != nil {
			c.ClientBodyConfig.XqhId = htmlquery.InnerText(node)
		} else {
			return errors.New("xqh_id获取失败")
		}

		if node := htmlquery.Find(doc, `//a[@role="tab"]/@onclick`); len(node) > 0 {
			err := AnalysisXkkzId(node)
			if err != nil {
				return err
			}
		} else {
			return errors.New("当前不属于选课阶段")
		}

		return nil
	}

	url := fmt.Sprintf("%s/xsxk/zzxkyzb_cxZzxkYzbIndex.html?gnmkdm=%s&layout=default", c.BaseURL, vars.GetGnmkdmSelect())
	result, _, err := c.Get(url, nil)
	if err != nil {
		return err
	}
	if isSessionExpired(result) {
		return errors.New("可能登录过期")
	}
	resStr := string(result)
	if strings.Contains(resStr, "当前不属于选课阶段") ||
		strings.Contains(resStr, "未到选课时间") ||
		strings.Contains(resStr, "目前还不能选课") ||
		strings.Contains(resStr, "选课尚未开始") ||
		strings.Contains(resStr, "不在选课时间段") {
		return errors.New("当前不属于选课阶段")
	}

	doc, err := htmlquery.Parse(strings.NewReader(resStr))
	if err != nil {
		return err
	}
	err = AnalysisConfig(doc)
	if err != nil {
		return err
	}

	return nil
}

// GetDoJxbId 获取do_jxb_id
func (c *Client) GetDoJxbId(req *GetDoJxbIdReq) ([]GetDoJxbIdResp, error) {
	url := fmt.Sprintf("%s/xsxk/zzxkyzbjk_cxJxbWithKchZzxkYzb.html?gnmkdm=%s", c.BaseURL, vars.GetGnmkdmSelect())
	formData := req.ToFormData()
	result, _, err := c.Post(url, formData.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if isSessionExpired(result) {
		return nil, errors.New("可能登录过期")
	}

	var doJxbIdResp []GetDoJxbIdResp
	err = json.Unmarshal(result, &doJxbIdResp)
	if err != nil {
		return nil, err
	}

	return doJxbIdResp, nil
}

// SelectCourse 选课
func (c *Client) SelectCourse(req *SelectCourseReq) (*SelectCourseResq, error) {
	url := fmt.Sprintf("%s/xsxk/zzxkyzbjk_xkBcZyZzxkYzb.html?gnmkdm=%s", c.BaseURL, vars.GetGnmkdmSelect())
	formData := req.ToFormData()
	result, _, err := c.Post(url, formData.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if isSessionExpired(result) {
		return nil, errors.New("可能登录过期")
	}

	var selectCourseResq SelectCourseResq
	err = json.Unmarshal(result, &selectCourseResq)
	if err != nil {
		return nil, err
	}

	return &selectCourseResq, nil
}

// CancelCourse 退课
func (c *Client) CancelCourse(req *CancelCourseReq) (string, error) {
	url := fmt.Sprintf("%s/xsxk/zzxkyzb_tuikBcZzxkYzb.html?gnmkdm=%s", c.BaseURL, vars.GetGnmkdmSelect())
	formData := req.ToFormData()
	result, _, err := c.Post(url, formData.Encode(), nil)
	if err != nil {
		return "", err
	}
	if isSessionExpired(result) {
		return "", errors.New("可能登录过期")
	}

	return string(result), nil
}

// SearchCourse 搜索课程
func (c *Client) SearchCourse(req *SearchCourseReq) (*SearchCourseResp, error) {
	url := fmt.Sprintf("%s/xsxk/zzxkyzb_cxZzxkYzbPartDisplay.html?gnmkdm=%s", c.BaseURL, vars.GetGnmkdmSelect())
	formData := req.ToFormData()
	result, _, err := c.Post(url, formData.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if isSessionExpired(result) {
		return nil, errors.New("可能登录过期")
	}

	var searchCourseResp SearchCourseResp
	err = json.Unmarshal(result, &searchCourseResp)
	if err != nil {
		return nil, err
	}

	return &searchCourseResp, nil
}

// GetStuInfo 获取学生信息/课表
func (c *Client) GetStuInfo() error {
	url := fmt.Sprintf("%s/kbcx/xskbcx_cxXsgrkb.html?gnmkdm=%s", c.BaseURL, vars.DefaultGnmkdmKb)
	result, _, err := c.Get(url, nil)
	if err != nil {
		return err
	}
	if isSessionExpired(result) {
		return errors.New("可能登录过期")
	}

	var stuInfoResp GetStuInfoResp
	err = json.Unmarshal(result, &stuInfoResp)
	if err != nil {
		return err
	}
	c.NjdmIDXs = stuInfoResp.Xsxx.NJDMID
	c.ZyhIDXs = stuInfoResp.Xsxx.ZYHID
	return nil
}

// GetZyhIdByBh 按班级查询专业号
func (c *Client) GetZyhIdByBh(id string) (string, error) {
	url := fmt.Sprintf("%s/xtgl/comm_cxBjdmList.html?&bh=%s", c.BaseURL, id)
	result, _, err := c.Get(url, nil)
	if err != nil {
		return "", err
	}
	if isSessionExpired(result) {
		return "", errors.New("可能登录过期")
	}
	var resp GetZyhIdByBhResp
	err = json.Unmarshal(result, &resp)
	if err != nil {
		return "", err
	}
	if len(resp) == 0 {
		return "", errors.New("未查询到该班级的专业号")
	}
	return resp[0].ZyhID, nil
}
