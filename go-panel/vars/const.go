package vars

import "os"

const (
	DefaultBaseURL      = "https://jwxt.zjjhkm.edu.cn/jwglxt"
	DefaultGnmkdmSelect = "N253512" // 自主选课
	DefaultGnmkdmTask   = "N1548"   // 任务落实
	DefaultGnmkdmKb     = "N2151"   // 课表查询
)

// GetBaseURL 获取当前教务基础URL
func GetBaseURL() string {
	if u := os.Getenv("BASE_URL"); u != "" {
		return u
	}
	return DefaultBaseURL
}

// GetGnmkdmSelect 获取选课功能代码
func GetGnmkdmSelect() string {
	if v := os.Getenv("GNMKDM_SELECT"); v != "" {
		return v
	}
	return DefaultGnmkdmSelect
}

var (
	// 不需要debug的url
	NoDebugUrl = map[string]bool{
		DefaultBaseURL + "/xtgl/login_slogin.html":                                                 true,
		DefaultBaseURL + "/rwlscx/rwlscx_cxRwlsIndex.html?doType=query&gnmkdm=" + DefaultGnmkdmTask: true,
		DefaultBaseURL + "/xsxk/zzxkyzb_cxZzxkYzbIndex.html?gnmkdm=" + DefaultGnmkdmSelect + "&layout=default": true,
	}
)

// Version 当前版本
const Version = "v1.0.0-jin"
