package course

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"jin-grabber/client"
	"jin-grabber/config"
	"jin-grabber/log"
)

// GetDoJxbId 获取doJxbId
func GetDoJxbId(c *client.Client, KchId string, JxbId string, Kklxdm string, NjdmId string, XueNian string, Xqm string) (string, error) {
	// 检查c.ClientBodyConfig是否为nil，测试用
	if c.ClientBodyConfig == nil {
		return "", errors.New("ClientBodyConfig未初始化")
	}

	// 设置请求参数
	req := &client.GetDoJxbIdReq{
		BklxID:   "0",
		NjdmID:   NjdmId,
		Xkxnm:    XueNian,
		Xkxqm:    Xqm,
		Kklxdm:   Kklxdm,
		KchID:    KchId,
		XkkzID:   c.ClientBodyConfig.XkkzId[Kklxdm],
		Xsbj:     c.ClientBodyConfig.Xsbj,
		Ccdm:     c.ClientBodyConfig.Ccdm,
		Xz:       c.ClientBodyConfig.Xz,
		Mzm:      c.ClientBodyConfig.Mzm,
		Xslbdm:   c.ClientBodyConfig.Xslbdm,
		Xbm:      c.ClientBodyConfig.Xbm,
		BhID:     c.ClientBodyConfig.BhId,
		ZyfxID:   c.ClientBodyConfig.ZyfxId,
		JgID:     c.ClientBodyConfig.JgId,
		XqhID:    c.ClientBodyConfig.XqhId,
		NjdmIDXs: c.NjdmIDXs,
		ZyhIDXs:  c.ZyhIDXs,
	}

	// 发送请求
	resp, err := c.GetDoJxbId(req)
	if err != nil {
		return "", err
	}

	// 解析doJxbId
	for _, v := range resp {
		if v.JxbID == JxbId {
			return v.DoJxbID, nil
		}
	}

	return "", errors.New("doJxbId不存在，未查询到该课程")

}

// SelectCourse 选课（干跑模式只记录不提交；选课失败返回 error 供上层标记状态）
func SelectCourse(c *client.Client, lg *log.Logger, JxbIds string, KchId string, Kklxdm string, Jxbzc string, cfg *config.Config) error {
	if cfg.DryRun == "1" {
		lg.Info("【干跑】跳过提交选课请求: ", KchId)
		return nil
	}

	// 设置请求参数
	req := &client.SelectCourseReq{
		JxbIDs: JxbIds,
		KchID:  KchId,
		Qz:     "0",
		XkkzID: c.ClientBodyConfig.XkkzId[Kklxdm],
	}

	// 若为主修课程
	if Kklxdm == "01" {
		if cfg.CrossGradeEnabled == "1" {
			req.NjdmID = "20" + Jxbzc[0:2]
			bh := strings.Split(Jxbzc, ";")[0]
			ZyhID, err := c.GetZyhIdByBh(bh)
			if err != nil {
				return err
			}
			req.ZyhID = ZyhID
		} else {
			req.NjdmID = c.NjdmIDXs
			req.ZyhID = c.ZyhIDXs
		}
	}

	req.NjdmIDXs = c.NjdmIDXs
	req.ZyhIDXs = c.ZyhIDXs

	// 发送请求
	result, err := c.SelectCourse(req)
	if err != nil {
		return err
	}

	if result.Flag == "1" {
		lg.Info("选课成功")
		return nil
	} else if result.Flag == "0" {
		lg.Error("选课失败: ", result.Msg)
		return fmt.Errorf("选课失败: %s", result.Msg)
	}
	lg.Error("选课失败: 人数可能已满")
	return errors.New("选课失败: 人数可能已满")
}

// CancelCourse 退课（干跑模式只记录不提交；退课失败返回 error）
func CancelCourse(c *client.Client, lg *log.Logger, JxbIds string, KchId string, XueNian string, Xqm string, cfg *config.Config) error {
	if cfg.DryRun == "1" {
		lg.Info("【干跑】跳过提交退课请求: ", KchId)
		return nil
	}

	// 设置请求参数
	req := &client.CancelCourseReq{
		JxbIDs: JxbIds,
		KchID:  KchId,
		Xkxnm:  XueNian,
		Xkxqm:  Xqm,
	}

	// 发送请求
	result, err := c.CancelCourse(req)
	if err != nil {
		return err
	}

	if result == "\"1\"" {
		lg.Info("退课成功(可能？)")
		return nil
	}
	lg.Error("退课失败：", result)
	return fmt.Errorf("退课失败: %s", result)
}

// HandleCourse 处理课程（查找教学班 → 获取doJxbId → 选课/退课）
func HandleCourse(c *client.Client, cfg *config.Config, lg *log.Logger, course *client.GetCourseResp, CourseName string, SelectFlag interface{}) error {
	if course == nil || len(course.Items) == 0 {
		return errors.New("教学班名称: " + CourseName + " 不存在")
	}
	for _, v := range course.Items {
		if v.Jxbmc == CourseName {
			// 更改Kklxdm
			Kklxdm := v.Kklxmc
			if Kklxdm == "主修课程" || strings.Contains(Kklxdm, "主修") || strings.Contains(Kklxdm, "必修") {
				Kklxdm = "01"
			} else if Kklxdm == "通识选修课" || strings.Contains(Kklxdm, "通识") || strings.Contains(Kklxdm, "公选") || strings.Contains(Kklxdm, "选修") {
				Kklxdm = "10"
			} else if Kklxdm == "体育分项" || strings.Contains(Kklxdm, "体育") {
				Kklxdm = "05"
			} else if Kklxdm == "特殊课程" || strings.Contains(Kklxdm, "特殊") {
				Kklxdm = "09"
			} else if Kklxdm != "" {
				lg.Warn("未严格匹配已知课程类型文案: ", v.Kklxmc, "，尝试默认01")
				Kklxdm = "01"
			} else {
				Kklxdm = "01"
			}

			// 打印课程信息
			lg.Info("课程名称: ", v.Kcmc)
			lg.Info("上课时间: ", v.Sksj)

			// 设置NjdmId
			NjdmId := "20" + c.ClientBodyConfig.BhId[0:2]

			// 设置Xqm
			Xqm := cfg.Time.XueQi
			if Xqm == "1" {
				Xqm = "3"
			} else if Xqm == "2" {
				Xqm = "12"
			} else {
				return errors.New("学期格式错误")
			}

			// 获取doJxbId
			doJxbId, err := GetDoJxbId(c, v.KchID, v.JxbID, Kklxdm, NjdmId, cfg.Time.XueNian, Xqm)
			if err != nil {
				return err
			}

			// 选课
			if SelectFlag == "1" {
				err = SelectCourse(c, lg, doJxbId, v.KchID, Kklxdm, v.Jxbzc, cfg)
				if err != nil {
					return err
				}
			} else {
				// 退课
				err = CancelCourse(c, lg, doJxbId, v.KchID, cfg.Time.XueNian, Xqm, cfg)
				if err != nil {
					return err
				}
			}

			return nil
		}
	}

	lg.Warn("教学班名称: ", CourseName, " 于本地课程列表不存在  将在线获取课程信息...")
	// 在线获取课程
	onlineCourse, _, err := GetCourseOnline(c, cfg, CourseName)
	if err != nil {
		return err
	}

	return HandleCourse(c, cfg, lg, onlineCourse, CourseName, SelectFlag)
}

// SaveClientBodyConfig 保存选课配置缓存到账号数据目录
func SaveClientBodyConfig(workDir string, c *client.Client) error {
	clientBodyConfig := c.ClientBodyConfig
	bytes, err := json.Marshal(clientBodyConfig)
	if err != nil {
		return err
	}

	err = os.WriteFile(workDir+"/ClientBodyConfig.json", bytes, 0666)
	if err != nil {
		return err
	}

	return nil
}

// ReadClientBodyConfig 从账号数据目录读取选课配置缓存
func ReadClientBodyConfig(workDir string, c *client.Client) error {
	bytes, err := os.ReadFile(workDir + "/ClientBodyConfig.json")
	if err != nil {
		return err
	}

	var clientBodyConfig client.ClientBodyConfig
	err = json.Unmarshal(bytes, &clientBodyConfig)
	if err != nil {
		return err
	}

	c.ClientBodyConfig = &clientBodyConfig

	return nil
}
