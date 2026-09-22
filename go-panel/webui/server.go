// Package webui 抢课面板 HTTP 服务：内嵌单页 UI + REST API
package webui

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"jin-grabber/client"
	"jin-grabber/engine"
	"jin-grabber/log"
	"jin-grabber/store"
)

//go:embed static/index.html
var indexHTML []byte

// Server 面板服务
type Server struct {
	mgr *engine.Manager
	st  *store.Store
	lg  *log.Logger
}

// Serve 启动面板 HTTP 服务（阻塞）
func Serve(port int, mgr *engine.Manager) error {
	s := &Server{mgr: mgr, st: mgr.Store(), lg: log.New("面板")}
	mux := http.NewServeMux()
	s.routes(mux)
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	s.lg.Info("面板监听: http://" + addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.mgr.Overview())
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.st.Global())
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var g store.GlobalSettings
		if !decode(w, r, &g) {
			return
		}
		if err := s.st.SetGlobal(g); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.lg.Info("全局设置已保存 (模式: ", g.Mode, " 开始: ", g.StartTime, " 干跑: ", g.DryRun, ")")
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.st.Accounts())
	})

	mux.HandleFunc("POST /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		var req engine.AddAccountRequest
		if !decode(w, r, &req) {
			return
		}
		acc, err := s.mgr.AddAccount(req)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, acc)
	})

	mux.HandleFunc("PUT /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		var acc store.Account
		if !decode(w, r, &acc) {
			return
		}
		acc.ID = r.PathValue("id")
		if err := s.mgr.UpdateAccount(&acc); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("DELETE /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.mgr.DeleteAccount(r.PathValue("id")); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// 账号操作
	s.accountAction(mux, "POST", "start", func(e *engine.Engine, _ []byte) (any, error) {
		return map[string]any{"ok": true}, e.Arm("手动启动")
	})
	s.accountAction(mux, "POST", "stop", func(e *engine.Engine, _ []byte) (any, error) {
		e.Stop()
		return map[string]any{"ok": true}, nil
	})
	s.accountAction(mux, "POST", "login-test", func(e *engine.Engine, _ []byte) (any, error) {
		return map[string]any{"ok": true}, e.LoginTest()
	})
	s.accountAction(mux, "POST", "refresh-course", func(e *engine.Engine, _ []byte) (any, error) {
		n, err := e.RefreshCourseLib()
		return map[string]any{"ok": err == nil, "count": n}, err
	})
	s.accountAction(mux, "POST", "grab-one", func(e *engine.Engine, body []byte) (any, error) {
		var req struct {
			Course string `json:"course"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Course == "" {
			return nil, errors.New("缺少 course 参数")
		}
		return map[string]any{"ok": true}, e.GrabOne(req.Course)
	})

	mux.HandleFunc("PUT /api/accounts/{id}/courses", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Courses [][2]string `json:"courses"`
		}
		if !decode(w, r, &req) {
			return
		}
		if err := s.st.ReplaceCourses(r.PathValue("id"), req.Courses); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// 课程库摘要（编辑器自动补全与余量展示）
	mux.HandleFunc("GET /api/accounts/{id}/lib", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		acc := s.st.Account(id)
		if acc == nil {
			writeErr(w, http.StatusNotFound, "账号不存在")
			return
		}
		lib, err := readCourseLib(s.st.ResolveCoursePath(id))
		if err != nil {
			writeJSON(w, map[string]any{"items": []any{}, "error": err.Error()})
			return
		}
		type item struct {
			Jxbmc  string `json:"jxbmc"`
			Kcmc   string `json:"kcmc"`
			Sksj   string `json:"sksj"`
			Kklxmc string `json:"kklxmc"`
			Jxbrl  int    `json:"jxbrl"`
			Xkrs   int    `json:"xkrs"`
		}
		out := make([]item, 0, len(lib.Items))
		for _, it := range lib.Items {
			out = append(out, item{it.Jxbmc, it.Kcmc, it.Sksj, it.Kklxmc, it.Jxbrl, it.Xkrs})
		}
		writeJSON(w, map[string]any{"items": out})	})

	// 导出旧版格式 config.json
	mux.HandleFunc("GET /api/accounts/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		acc := s.st.Account(r.PathValue("id"))
		if acc == nil {
			writeErr(w, http.StatusNotFound, "账号不存在")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=config_"+acc.ID+".json")
		b, _ := json.MarshalIndent(acc.Config, "", "    ")
		_, _ = w.Write(b)
	})

	// 导入旧版 config.json
	mux.HandleFunc("POST /api/import-legacy", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Label  string          `json:"label"`
			Config json.RawMessage `json:"config"`
		}
		if !decode(w, r, &req) {
			return
		}
		acc, err := s.mgr.ImportLegacy(req.Config, req.Label)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, acc)
	})

	mux.HandleFunc("POST /api/start-all", func(w http.ResponseWriter, r *http.Request) {
		go s.mgr.StartAll("手动启动全部")
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/stop-all", func(w http.ResponseWriter, r *http.Request) {
		s.mgr.StopAll()
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/email-test", func(w http.ResponseWriter, r *http.Request) {
		if err := s.mgr.SendTestEmail(); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		tagFilter := r.URL.Query().Get("tag")
		seq, entries := log.Entries(since)
		if tagFilter != "" {
			filtered := make([]log.Entry, 0, len(entries))
			for _, e := range entries {
				if e.Tag == tagFilter {
					filtered = append(filtered, e)
				}
			}
			writeJSON(w, map[string]any{"seq": seq, "entries": filtered})
			return
		}
		writeJSON(w, map[string]any{"seq": seq, "entries": entries})
	})

	mux.HandleFunc("POST /api/logs/clear", func(w http.ResponseWriter, r *http.Request) {
		log.Clear()
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"status": "ok", "time": time.Now().Format("2006-01-02 15:04:05")})
	})
}

// accountAction 注册 POST /api/accounts/{id}/{action}
func (s *Server) accountAction(mux *http.ServeMux, method, action string, fn func(e *engine.Engine, body []byte) (any, error)) {
	mux.HandleFunc(method+" /api/accounts/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
		e, err := s.mgr.EngineFor(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		body := readBody(r)
		result, err := fn(e, body)
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, result)
	})
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	return b
}

func readCourseLib(path string) (*client.GetCourseToExcelResp, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lib client.GetCourseToExcelResp
	if err := json.Unmarshal(b, &lib); err != nil {
		return nil, err
	}
	return &lib, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}
