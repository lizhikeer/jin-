package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"jin-grabber/client"
	"jin-grabber/config"
	"jin-grabber/pkg/course"
	"jin-grabber/pkg/login"
	"jin-grabber/store"
)

// run 引擎主流程：定时模式先挂机等待（含到点前刷新会话），蹲课模式立即轮询
func (e *Engine) run(ctx context.Context, trigger string) {
	g := e.mgr.st.Global()
	acc := e.mgr.st.Account(e.id)
	if acc == nil {
		e.setError("账号不存在")
		return
	}
	cfg := acc.Config.Clone()
	cfg.DryRun = bool01(g.DryRun)
	cfg.WaitCourse.Enabled = bool01(g.Mode == "wait")
	if g.Interval > 0 {
		cfg.WaitCourse.Interval = g.Interval
	}
	e.resetCourseStatus(cfg)

	if len(cfg.CourseKeys()) == 0 {
		e.setError("课程列表为空，请先在账号编辑中添加课程")
		return
	}

	e.lg.Info("任务启动 (", trigger, ") 模式: ", g.Mode, " 干跑: ", g.DryRun)

	if g.Mode == "wait" {
		e.runWaitAll(ctx, cfg)
		return
	}
	e.runKill(ctx, cfg, g)
}

func (e *Engine) label() string {
	if a := e.mgr.st.Account(e.id); a != nil {
		return a.Label
	}
	return e.id
}

// loginWithRetry 双重保险登录；失败按 retry 间隔重试直到成功或 ctx 取消
func (e *Engine) loginWithRetry(ctx context.Context, cfg *config.Config, retry time.Duration) (*client.Client, bool) {
	for {
		c, err := login.Login(cfg, e.lg)
		if err == nil {
			_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")
			return c, true
		}
		_ = e.mgr.st.SetSessionState(e.id, "expired", "password")
		e.lg.Error("登录失败（", retry.String(), "后重试）: ", err)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(retry):
		}
	}
}

// loadLibWithRetry 获取课程库（优先本地 course.json，失败在线拉取），重试直到成功
func (e *Engine) loadLibWithRetry(ctx context.Context, c *client.Client, cfg *config.Config) (*client.GetCourseResp, bool) {
	for {
		lib, err := course.GetCourse(c, cfg, e.lg, e.mgr.st.ResolveCourseDir(e.id))
		if err == nil {
			return lib, true
		}
		e.lg.Error("获取课程库失败: ", err)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(5 * time.Second):
		}
	}
}

// waitUntil 等待到指定时刻，返回 false 表示被取消
func (e *Engine) waitUntil(ctx context.Context, t time.Time) bool {
	d := time.Until(t)
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// runKill 定时抢课：挂机等待 → 到点按顺序冲一轮 → 失败可选自动转蹲课
func (e *Engine) runKill(ctx context.Context, cfg *config.Config, g store.GlobalSettings) {
	// 1. 启动时先验证一遍登录（提前缓存 cookie；系统未开放失败不打断挂机）
	e.setState(StateGrabbing, "启动中：验证登录并缓存会话...")
	if _, err := login.Login(cfg, e.lg); err != nil {
		_ = e.mgr.st.SetSessionState(e.id, "expired", "password")
		e.lg.Warn("启动登录验证失败（到点前会自动重试）: ", err)
	} else {
		_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")
	}

	// 2. 等待开始时间
	if g.StartTime != "" {
		t, err := time.ParseInLocation("2006-01-02 15:04:05", g.StartTime, cstLoc)
		if err != nil {
			e.setError("开始时间格式错误: " + err.Error())
			return
		}
		if t.After(time.Now()) {
			e.setState(StateWaiting, "等待选课开始 "+g.StartTime)
			e.lg.Info("选课开始时间: ", t.Format("2006-01-02 15:04:05"))

			// 到点前 N 分钟刷新会话，保证开抢瞬间 cookie 新鲜
			if g.RefreshBeforeMin > 0 {
				refreshAt := t.Add(-time.Duration(g.RefreshBeforeMin) * time.Minute)
				if refreshAt.After(time.Now()) {
					if !e.waitUntil(ctx, refreshAt) {
						e.setStopped()
						return
					}
					e.lg.Info("距开抢不足 ", g.RefreshBeforeMin, " 分钟，刷新会话...")
					if c, err := login.Login(cfg, e.lg); err == nil {
						_ = e.mgr.st.SetSession(e.id, cfg.Cookies, "valid", "login")
						_ = c
					} else {
						e.lg.Error("会话刷新失败（将沿用现有会话）: ", err)
					}
					e.setState(StateWaiting, "会话已刷新，等待开抢 "+g.StartTime)
				}
			}
			if !e.waitUntil(ctx, t) {
				e.setStopped()
				return
			}
		}
	}

	// 3. 到点：登录 + 课程库 + 选课配置（循环重试直到就绪）
	e.setState(StateGrabbing, "正在登录并准备选课环境...")
	c, ok := e.loginWithRetry(ctx, cfg, 5*time.Second)
	if !ok {
		e.setStopped()
		return
	}
	lib, ok := e.loadLibWithRetry(ctx, c, cfg)
	if !ok {
		e.setStopped()
		return
	}
	for {
		if err := e.ensureClientBody(c); err == nil {
			break
		} else {
			e.lg.Warn("等待选课开放: ", err)
		}
		select {
		case <-ctx.Done():
			e.setStopped()
			return
		case <-time.After(2 * time.Second):
		}
	}

	// 4. 按顺序执行一轮选/退课
	e.setState(StateGrabbing, "定时抢课执行中")
	failed := []string{}
	for _, k := range cfg.CourseKeys() {
		if ctx.Err() != nil {
			e.setStopped()
			return
		}
		action := cfg.CourseAction(k)
		e.setCourse(k, "processing", "")
		e.lg.Info("----------------------------------------")
		e.lg.Info("正在处理课程: ", k)
		err := course.HandleCourse(c, cfg, e.lg, lib, k, action)
		if err != nil && err.Error() == "可能登录过期" {
			e.lg.Warn("登录过期，重新登录后重试该课程...")
			nc, ok := e.loginWithRetry(ctx, cfg, 5*time.Second)
			if !ok {
				e.setStopped()
				return
			}
			c = nc
			if err := e.ensureClientBody(c); err != nil {
				e.lg.Warn("选课配置刷新失败: ", err)
			}
			err = course.HandleCourse(c, cfg, e.lg, lib, k, action)
		}
		if err != nil {
			e.setCourse(k, "failed", err.Error())
			if action == "1" {
				failed = append(failed, k)
			}
			continue
		}
		e.setCourse(k, "success", "")
	}

	// 5. 汇总 + 通知
	summary := e.summaryText(cfg)
	prefix := ""
	if g.DryRun {
		prefix = "【干跑】"
	}
	e.mgr.notify(fmt.Sprintf("%s【金华科贸抢课】%s 定时抢课结束", prefix, e.label()), summary)

	// 6. 失败转蹲课
	if g.FallbackToWait && len(failed) > 0 {
		e.lg.Warn(len(failed), " 门选课课程未成功，自动转入蹲课模式继续捡漏: ", fmt.Sprint(failed))
		for _, name := range failed {
			e.setCourse(name, "waiting", "抢课未成功，转入蹲课")
		}
		e.setState(StateWaitingCourse, "抢课失败转蹲课捡漏中")
		e.runWaitCourses(ctx, cfg, c, lib, failed)
		return
	}
	e.setState(StateDone, summary)
}

// ---- 蹲课模式下的"退课条目"（动作=0）支持 ----
//
// 原实现里蹲课模式只会轮询动作=1 的课程，动作=0 的条目被完全忽略。
// 但有些课是互斥的（例如时间冲突，两门不能同时选），必须先退掉旧课才能选上新目标，
// 于是把动作=0 的条目当作"蹲到目标之后才执行的退课清单"：
//   before   蹲到余量 → 先执行退课条目 → 再提交选课（默认）
//   conflict 先提交选课 → 返回冲突类错误时才退课并重试
//   off      完全不执行（旧行为）
// 清单在 run 开始时快照，避免与"蹲选成功后把该门置 0"的既有逻辑互相干扰。

type dropPlan struct {
	mu      sync.Mutex
	pending []string        // 待执行的退课条目（保序）
	done    map[string]bool // 本轮已处理过的
}

func newDropPlan(cfg *config.Config) *dropPlan {
	dp := &dropPlan{done: map[string]bool{}}
	for _, k := range cfg.CourseKeys() {
		if cfg.CourseAction(k) == "0" {
			dp.pending = append(dp.pending, k)
		}
	}
	return dp
}

func (dp *dropPlan) remaining() []string {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	out := make([]string, 0, len(dp.pending))
	for _, n := range dp.pending {
		if !dp.done[n] {
			out = append(out, n)
		}
	}
	return out
}

// runDropPlan 按课程表顺序执行尚未处理的退课条目（内部串行，多个蹲课协程只会真正跑一次）
func (e *Engine) runDropPlan(c *client.Client, cfg *config.Config, lib *client.GetCourseResp, dp *dropPlan, why string) ([]string, error) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if len(dp.pending) == 0 {
		return nil, nil
	}
	var dropped []string
	for _, name := range dp.pending {
		if dp.done[name] {
			continue
		}
		dp.done[name] = true // 每个条目一轮只尝试一次，避免反复打教务
		e.lg.Info("----------------------------------------")
		e.lg.Info("退课条目 [", why, "]: ", name)
		e.setCourse(name, "processing", why+"：退课中")
		err := course.HandleCourse(c, cfg, e.lg, lib, name, "0")
		if err != nil {
			if err.Error() == "可能登录过期" {
				e.setCourse(name, "waiting", "退课遇登录过期，待重登")
				return dropped, err
			}
			e.setCourse(name, "failed", "退课失败: "+err.Error())
			e.lg.Error("退课失败: ", name, " ", err)
			continue
		}
		e.setCourse(name, "success", "退课完成")
		dropped = append(dropped, name)
		e.lg.Info("退课成功: ", name)
	}
	return dropped, nil
}

// rollbackDrops 目标课最终没抢到，尽力把刚退掉的课重新选回来，避免"两头空"
func (e *Engine) rollbackDrops(c *client.Client, cfg *config.Config, lib *client.GetCourseResp, dropped []string) {
	for _, n := range dropped {
		e.lg.Warn("目标课未抢到，回滚重选被退课程: ", n)
		e.setCourse(n, "processing", "回滚重选中")
		if err := course.HandleCourse(c, cfg, e.lg, lib, n, "1"); err != nil {
			e.setCourse(n, "failed", "回滚失败: "+err.Error())
			e.lg.Error("回滚重选失败: ", n, " ", err)
			continue
		}
		e.setCourse(n, "success", "回滚重选成功")
		e.lg.Info("回滚重选成功: ", n)
	}
}

const (
	selectGap = 1500 * time.Millisecond
)

// selectWithRetry 提交选课，失败则间隔重试（教务系统在刚退课时可能有短暂状态延迟）
func (e *Engine) selectWithRetry(ctx context.Context, c *client.Client, cfg *config.Config, lib *client.GetCourseResp, name string, tries int) error {
	var err error
	for i := 1; i <= tries; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = course.HandleCourse(c, cfg, e.lg, lib, name, "1")
		if err == nil {
			return nil
		}
		if err.Error() == "可能登录过期" {
			return err
		}
		if i < tries {
			e.lg.Warn(name, " 选课失败(", i, "/", tries, "): ", err, "，稍后重试")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(selectGap):
			}
		}
	}
	return err
}

var conflictWords = []string{"冲突", "已选", "同一时间", "重复", "已存在", "互斥", "时间重叠", "相冲", "不能同时"}

// isConflictErr 判断选课失败是否属于"与已选课程冲突"这一类
func isConflictErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, w := range conflictWords {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
}

// runWaitAll 蹲课模式入口：登录就绪后对课程表动作=1 的全部课程并发蹲守
func (e *Engine) runWaitAll(ctx context.Context, cfg *config.Config) {
	e.setState(StateWaitingCourse, "蹲课捡漏启动中...")
	c, ok := e.loginWithRetry(ctx, cfg, 15*time.Second)
	if !ok {
		e.setStopped()
		return
	}
	lib, ok := e.loadLibWithRetry(ctx, c, cfg)
	if !ok {
		e.setStopped()
		return
	}
	if err := e.ensureClientBody(c); err != nil {
		// 蹲课查余量不依赖选课配置，但选课动作需要；先告警继续
		e.lg.Warn("选课配置暂不可用（选课开放后自动重试）: ", err)
	}
	e.setState(StateWaitingCourse, "蹲课捡漏中")
	e.runWaitCourses(ctx, cfg, c, lib, nil)
}

// runWaitCourses 蹲课轮询指定课程（names=nil 表示课程表动作=1 的全部课程）
func (e *Engine) runWaitCourses(ctx context.Context, cfg *config.Config, c *client.Client, lib *client.GetCourseResp, names []string) {
	g := e.mgr.st.Global()
	interval := time.Duration(g.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	expireRetried := 0

	// 蹲课模式下的退课条目快照：整轮 run 只执行一次
	dp := newDropPlan(cfg)
	dropMode := g.WaitDropMode
	if dropMode != "before" && dropMode != "conflict" {
		dropMode = "off"
	}
	if dropMode != "off" && len(dp.pending) > 0 {
		e.lg.Info("蹲课自动退课已启用（策略=", dropMode, "），待执行退课条目 ", len(dp.pending), " 条: ", fmt.Sprint(dp.pending))
	}

	for {
		// 计算本轮待蹲课程（动作仍为1 且未成功）
		var todo []string
		if names == nil {
			for _, k := range cfg.CourseKeys() {
				if cfg.CourseAction(k) == "1" {
					todo = append(todo, k)
				}
			}
		} else {
			for _, n := range names {
				if cfg.CourseAction(n) == "1" {
					todo = append(todo, n)
				}
			}
		}
		if len(todo) == 0 {
			e.setState(StateDone, "蹲课结束：全部目标课程已完成")
			e.lg.Info("全部目标课程已处理完毕")
			return
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		expired := false

		for _, name := range todo {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				ok, err := course.GetIsCourseOk(c, cfg, e.lg, lib, name)
				if err != nil {
					if err.Error() == "可能登录过期" {
						mu.Lock()
						expired = true
						mu.Unlock()
						return
					}
					e.setCourse(name, "waiting", "查询失败: "+err.Error())
					e.lg.Error(name, " 查询失败: ", err)
					return
				}
				if !ok {
					e.setCourse(name, "waiting", "暂无余量，继续蹲守")
					return
				}

				// 有余量，按策略立即行动
				e.lg.Info(name, " 出现余量，立即处理！")
				e.setCourse(name, "processing", "发现余量，提交选课中")

				// 策略=先退后选：互斥课程必须先退掉旧的，否则选课一定失败
				var dropped []string
				if dropMode == "before" && len(dp.remaining()) > 0 {
					var derr error
					dropped, derr = e.runDropPlan(c, cfg, lib, dp, "蹲到目标·先退课")
					if derr != nil {
						mu.Lock()
						expired = true
						mu.Unlock()
						return
					}
				}

				// 刚退过课，教务侧可能有短暂状态延迟，这时才多重试几次
				tries := 1
				if len(dropped) > 0 {
					tries = 3
				}
				err = e.selectWithRetry(ctx, c, cfg, lib, name, tries)

				// 策略=冲突再退：先试选，后端报冲突时才退课并重试
				if err != nil && dropMode == "conflict" && isConflictErr(err) && len(dp.remaining()) > 0 {
					e.lg.Warn(name, " 选课疑似冲突，改为先退课再重试: ", err)
					more, derr := e.runDropPlan(c, cfg, lib, dp, "选课冲突·改退课")
					if derr != nil {
						mu.Lock()
						expired = true
						mu.Unlock()
						return
					}
					dropped = append(dropped, more...)
					err = e.selectWithRetry(ctx, c, cfg, lib, name, 3)
				}

				if err != nil {
					if err.Error() == "可能登录过期" {
						mu.Lock()
						expired = true
						mu.Unlock()
						return
					}
					// 没抢到：尽力把刚退掉的课重新选回来
					if len(dropped) > 0 {
						e.rollbackDrops(c, cfg, lib, dropped)
					}
					e.setCourse(name, "waiting", "选课失败将继续蹲: "+err.Error())
					e.lg.Error(name, " 选课失败: ", err)
					return
				}
				e.setCourse(name, "success", "蹲选成功")
				_ = e.mgr.st.SetCourseAction(e.id, name, "0")
				body := name + " 已成功选课（请自行登录教务系统核对）"
				if len(dropped) > 0 {
					body += "\n已按课程表退课: " + strings.Join(dropped, ", ")
				}
				e.mgr.notify(fmt.Sprintf("【金华科贸抢课】%s 蹲选成功", e.label()), body)
			}(name)
		}
		wg.Wait()

		if ctx.Err() != nil {
			e.setStopped()
			return
		}

		if expired {
			expireRetried++
			if expireRetried > 200 {
				e.setError("反复登录过期，已停止蹲课")
				return
			}
			e.lg.Warn("检测到登录过期，重新登录...")
			nc, ok := e.loginWithRetry(ctx, cfg, 10*time.Second)
			if !ok {
				e.setStopped()
				return
			}
			c = nc
			if err := e.ensureClientBody(c); err != nil {
				e.lg.Warn("选课配置刷新失败: ", err)
			}
		}

		// 间隔等待
		select {
		case <-ctx.Done():
			e.setStopped()
			return
		case <-time.After(interval):
		}
	}
}

// summaryText 汇总各课程结果
func (e *Engine) summaryText(cfg *config.Config) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	success, failed := 0, 0
	lines := ""
	for _, k := range e.order {
		cs := e.courses[k]
		switch cs.State {
		case "success":
			success++
			lines += fmt.Sprintf("✔ %s %s\n", k, cs.Detail)
		case "failed":
			failed++
			lines += fmt.Sprintf("✘ %s %s\n", k, cs.Detail)
		default:
			lines += fmt.Sprintf("- %s (%s) %s\n", k, cs.State, cs.Detail)
		}
	}
	return fmt.Sprintf("成功 %d / 失败 %d\n%s", success, failed, lines)
}
