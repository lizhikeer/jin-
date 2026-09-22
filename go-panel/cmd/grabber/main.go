// HDU 抢课面板：多账号常驻引擎 + Web 管理页面（Docker 部署）
package main

import (
	"os"
	"path/filepath"
	"strconv"
	_ "time/tzdata" // 内嵌时区数据库，容器内 Asia/Shanghai 解析不依赖系统 tzdata

	"jin-grabber/engine"
	"jin-grabber/log"
	"jin-grabber/store"
	"jin-grabber/vars"
	"jin-grabber/webui"
)

func main() {
	vars.ShowPortal()

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	log.Init(filepath.Join(dataDir, "log_files"))

	st, err := store.Load(dataDir)
	if err != nil {
		log.Error("加载数据目录失败: ", err)
		os.Exit(1)
	}
	log.Info("数据目录: ", dataDir)

	mgr := engine.NewManager(st)

	port := 18080
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
			port = n
		}
	}

	if err := webui.Serve(port, mgr); err != nil {
		log.Error("面板服务退出: ", err)
		os.Exit(1)
	}
}
