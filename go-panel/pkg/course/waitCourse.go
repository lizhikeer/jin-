package course

import (
	"errors"

	"jin-grabber/client"
	"jin-grabber/config"
	"jin-grabber/log"
)

// GetIsCourseOk 检验课程是否有余量（蹲课轮询用）
func GetIsCourseOk(c *client.Client, cfg *config.Config, lg *log.Logger, course *client.GetCourseResp, CourseName string) (bool, error) {
	for _, v := range course.Items {
		if v.Jxbmc == CourseName {
			// 更改Kklxdm
			Kklxdm := v.Kklxmc
			if Kklxdm == "主修课程" {
				Kklxdm = "01"
			} else if Kklxdm == "通识选修课" {
				Kklxdm = "10"
			} else if Kklxdm == "体育分项" {
				Kklxdm = "05"
			} else if Kklxdm == "特殊课程" {
				Kklxdm = "09"
			} else {
				return false, errors.New("课程类型错误")
			}

			// 设置Xqm
			Xqm := cfg.Time.XueQi
			if Xqm == "1" {
				Xqm = "3"
			} else if Xqm == "2" {
				Xqm = "12"
			} else {
				return false, errors.New("学期格式错误")
			}

			// 获取课程是否有余量
			req := &client.SearchCourseReq{
				Xkxnm:      cfg.Time.XueNian,
				Xkxqm:      Xqm,
				Kklxdm:     Kklxdm,
				Kspage:     "1",
				Jspage:     "10",
				Yllist:     "1",
				Filterlist: CourseName,
				NjdmIDXs:   c.NjdmIDXs,
				ZyhIDXs:    c.ZyhIDXs,
			}

			// 发送请求
			result, err := c.SearchCourse(req)
			if err != nil {
				return false, err
			}

			// 检验是否有余量, TmpList是否长度为0
			if len(result.TmpList) == 0 {
				lg.Info(CourseName+" "+v.Kcmc+": ", "课程无余量")
				return false, nil
			}
			lg.Info(CourseName+" "+v.Kcmc+": ", "课程有余量")
			return true, nil
		}
	}
	return false, errors.New(CourseName + "课程不存在")
}
