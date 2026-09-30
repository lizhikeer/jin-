[README.md](https://github.com/user-attachments/files/32863578/README.md)
# 浙江金华科贸职业技术学院 · 自动抢课面板

适配浙江金华科贸职业技术学院正方教务系统（`https://jwxt.zjjhkm.edu.cn/jwglxt`）的自动选课工具。
**单个 Go 程序 + 内嵌 Web 管理面板**，支持多账号并发挂机。

```
go-panel/          Go 面板版（唯一实现）
启动面板.bat        Windows 一键启动
```

---

## 🚀 

### 第1步：启动面板

双击 **`启动面板.bat`**，浏览器会自动打开配置页面 `http://127.0.0.1:18080`。

### 第2步：添加账号

点 **【＋ 添加账号】**，填：
- **备注名**：随便填（如 "我的账号"）
- **学号**：你的教务系统学号
- **密码**：你的教务系统密码

点保存。

### 第3步：测试登录

在账号列表点 **【🧪 登录】**，显示绿色 ✅ 就代表成功了。

### 第4步：更新课程库

点 **【✏ 编辑】** → **【🔄 更新课程库】**，等待课程列表加载完成。

### 第5步：配置要抢的课

在课程表里填写你要抢的教学班名称（格式如 `(2026-2027-1)-课程号-班号`），
动作选 **「选课」**。

课程ID从哪里来？去教务系统选课界面 → F12 → Network → 找到课程信息就能看到。

### 第6步：先干跑测试

全局设置勾选 **【开启干跑模式 (DryRun)】** → 点 **【▶ 启动全部】**。
查看日志确认登录、查询、选课流程都正常。这步**不会实际提交选课**。

### 第7步：正式开抢

取消干跑 ✓ 再点 **【▶ 启动全部】**！程序会自动轮询，发现空位秒抢！

---

## 特性

- 💻 **免装环境**：编译产物是静态单文件，Windows 版已随仓库附带 `jin-grabber.exe`
- 👥 **多账号并发**：每个账号独立会话与课程表，错峰登录
- 🔐 **双重保险**：Cookie 自动持久化复用，失效时自动用正方账密（RSA PKCS#1 v1.5）重新登录
- 🛡️ **安全干跑**：DryRun 模式只查询解析、不提交选课，可先演练全流程
- 📊 **Web 控制台**：内嵌单页面板（默认 `18080`），实时倒计时、轮询间隔、SMTP 邮件提醒、实时日志
- 🔑 **登录鉴权**：面板自带登录页，会话 Cookie 为 HMAC 签名令牌
- 📚 **余量取自自主选课**：课程库查询**自主选课**列表接口，拿到的是当前选课轮次下**真实可选**的教学班
- 🔁 **开机自启 + 恢复挂机**：部署为 systemd 服务后，机器重启会自动拉起面板并恢复抢课任务

---

## 快速开始

### Windows

双击仓库根目录的 **`启动面板.bat`**（或 `go-panel/启动面板.bat`），
浏览器会自动打开 `http://127.0.0.1:18080`。

### Linux / NAS（推荐部署为常驻服务）

```bash
# 1) 编译
cd go-panel
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /opt/jin-grabber/jin-grabber ./cmd/grabber
# 需先 mkdir -p /opt/jin-grabber

# 2) 安装 systemd 单元
sudo tee /etc/systemd/system/jin-grabber.service >/dev/null <<'EOF'
[Unit]
# 崩溃后无限重试，不因短时间内多次失败而放弃拉起
StartLimitIntervalSec=0
Description=Jin Grabber - ZJJHKM auto course grabber panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=cjs
Group=Users
WorkingDirectory=/opt/jin-grabber
Environment=DATA_DIR=/var/lib/jin-grabber
Environment=PORT=18080
EnvironmentFile=/etc/jin-grabber.env
Environment=TZ=Asia/Shanghai
ExecStart=/opt/jin-grabber/jin-grabber
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

# 3) 面板登录口令（0600，root 可读即可）
sudo tee /etc/jin-grabber.env >/dev/null <<'EOF'
PANEL_USER=cjs
PANEL_PASS=换成你自己的口令
EOF
sudo chmod 600 /etc/jin-grabber.env

# 4) 启动并设为开机自启
sudo mkdir -p /var/lib/jin-grabber && sudo chown cjs:Users /var/lib/jin-grabber
sudo systemctl daemon-reload
sudo systemctl enable --now jin-grabber
systemctl status jin-grabber --no-pager
```

面板监听 `0.0.0.0:18080`（**IPv4 + IPv6 双栈**），局域网内直接访问
`http://<机器IP>:18080`；IPv6 环境用 `http://[<全局IPv6>]:18080`。

常用命令：

```bash
sudo systemctl restart jin-grabber     # 重启
journalctl -u jin-grabber -f           # 跟踪日志
```

---

## 面板使用流程

1. **添加账号**：点顶部 **【＋ 添加账号】**，填备注名、正方学号、密码，保存。
2. **测试登录**：账号列表点 **【🧪 登录】**。成功会显示绿色会话状态并缓存 Cookie；
   若提示失败，先确认学号密码能在浏览器正常登录教务系统。
3. **更新课程库**：账号行点 **【✏ 编辑】** → **【🔄 更新课程库】**。
   下拉提示会显示课程名、上课时间与余量标注。
4. **配置课程表**：在课程表里填写教学班名称（格式 `(2026-2027-1)-课程号-班号`），
   动作选「选课」或「退课」，可用上移/下移调整优先级。
5. **先干跑**：全局设置勾选 **【开启干跑模式 (DryRun)】**，
   点 **【▶ 启动全部】**，看日志确认登录、查询、命中教学班等环节都正常。
6. **正式挂机**：取消干跑，再点 **【▶ 启动全部】**。

> 【▶ 启动全部】会同时打开「重启后自动恢复挂机」开关；
> 【■ 停止全部】会把它关掉——**停过就保持停着**。

### 运行模式

| 模式 | 用途 |
| --- | --- |
| **定时抢课** | 适合整点开放选课。设好开始时间，程序会提前刷新会话并到点开抢 |
| **蹲课捡漏** | 适合选课开放后蹲余量。按设定间隔轮询，发现空位立即选入 |

蹲课模式下「退课」条目（动作 = 0）的执行策略见面板设置：`before` 先退后选（默认）、
`conflict` 先选、冲突时才退、`off` 忽略退课条目。

---

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATA_DIR` | `data` | 数据目录（账号、课程库、日志、密钥都在这里） |
| `PORT` | `18080` | 面板监听端口 |
| `PANEL_USER` | `cjs` | 面板登录账号 |
| `PANEL_PASS` | 见下 | 面板登录口令；未设置则生成随机口令 |
| `PANEL_SECRET` | 见下 | 会话签名密钥；未设置则持久化到数据目录 |
| `SELECT_ONLY_AVAILABLE` | 关 | 设为 `1` 时课程库只保留仍有余量的教学班 |
| `BASE_URL` | 本校教务地址 | 换学校时改这个 |
| `GNMKDM_SELECT` | `N253512` | 自主选课功能模块号 |
| `TZ` | 系统时区 | 建议 `Asia/Shanghai`（程序内已内嵌时区数据） |

### 数据目录内容

| 路径 | 说明 |
| --- | --- |
| `accounts.json` | 账号列表（**含学号与密码明文，务必不要外传**） |
| `global.json` | 全局设置（模式、间隔、干跑、自动恢复开关、SMTP） |
| `courses/_shared/course.json` | 共享课程库（按学年学期全校统一，只存一份） |
| `courses/_shared/*.xlsx` | 课程库 Excel 导出（含「余量」列） |
| `clientbody/<账号ID>.json` | 选课配置缓存（仅兜底非轮次字段） |
| `log_files/app.log` | 应用日志 |
| `log_files/debug.log` | 协议层原始请求/响应（排查问题用，增长较快） |
| `.panel_secret` | 会话签名密钥（0600） |
| `.panel_pass` | 未设置 `PANEL_PASS` 时生成的随机口令（0600） |

---

## 课程库数据来源

| 优先级 | 接口 | 说明 |
| --- | --- | --- |
| 1（默认） | `xsxk/zzxkyzb_cxZzxkYzbPartDisplay.html`（自主选课，`N253512`） | 当前轮次**真实可选**的教学班 |
| 2（兜底） | `rwlscx_cxRwlsIndex.html`（任务落实，`N1548`） | 自主选课不可用时回退计划表 |

- 按课程类型（`kklxdm`：主修 `01` / 体育 `05` / **板块课 `06`** / 特殊 `09` / 通识选修 `10`）
  分批查询，并逐步放大 `jspage` 窗口、按 `jxb_id` 合并去重，直到没有新记录。
- 默认**不过滤余量**：满员的课也会进课程库并标注，方便先配进课程表再蹲课。
  只想要有余量的课，设 `SELECT_ONLY_AVAILABLE=1`。
- 蹲课轮询同样查自主选课接口并带 `yl_list[0]=1`。

### 余量是怎么判定的

本站教务系统的列表接口**不返回容量字段**（`rwzxs` 是学时不是容量，`blzyl` 恒为 0），
所以无法靠数字算余量。程序的做法是：**全量拉取一次 + 再用 `yl_list[0]=1` 拉取一次**，
凡出现在后一次结果里的教学班即标记为「有余量」——服务端的这次过滤是可用性的权威依据。

面板下拉里的标注会按可用信息降级显示：

```
余量5        ← 接口给了余量数字
余量5/50     ← 接口给了容量，可算
有余量        ← 只有服务端的余量过滤结果（本站当前是这种）
已选42        ← 都没有时，退化为显示已选人数
```

---

## 针对本站教务系统的实测适配（2026-09）

本站的正方版本与通用实现有多处差异，代码已按实测行为适配：

| 问题 | 实测现象 | 处理 |
| --- | --- | --- |
| **「加密串错误」** | 列表接口缺 `xkkz_xh` 时返回 `{"msg":"加密串错误…","flag":"0"}`。`xkkz_xh` 是 **256 位十六进制（128 字节）**加密串，与选课页同生命周期、**每次加载都重新生成** | 请求携带 `xkkz_id` + `xkkz_xh`；每次运行都重新拉取选课页，**绝不缓存**这两个值 |
| 选课轮次只有一个页签 | 前端**不渲染页签**（源码注释「只有一个页签时，不显示页签」），靠 `role="tab"` + `queryCourse(...)` 解析会误判为「当前不属于选课阶段」 | 增加从 `firstKklxdm` / `firstKklxmc` / `firstXkkzId` / `firstXkkzXh` 隐藏域解析轮次的回退路径 |
| 学生信息接口 | `kbcx/xskbcx_cxXsgrkb.html` 用 **GET 无参**请求返回 `null`，年级/专业始终为空 | 改为 POST 并携带 `xnm` / `xqm`，可取得年级、专业号、姓名、班级、`XKKG` |
| 学生维度参数名 | 前端 JS 里 `njdm_id_xs` / `zyh_id_xs` 是**被注释掉的**，实际发 `njdm_id` / `zyh_id` | 按实测补齐参数（`_xs` 保留以兼容其他学校） |
| 课程类型代码 | 存在 `kklxdm=06`（板块课(体育板块)） | 已知类型集合与名称映射加入 `06` |
| 字段命名 | 已选人数是 `yxzrs`，类型名称是 `kclxmc` / `kzmc`（**没有** `kklxmc`） | 解析候选键名覆盖这些变体 |
| 分页 | `totalResult` 恒为 0，只靠总量判断会只拿到第一组（实测仅 8 条） | 放大窗口 + 按 `jxb_id` 合并去重，实测取到 **79 条** |
| 选课壳页 | `cxZzxkYzbDisplay.html` 只返回空壳「请使用上方的查询工具条查询所需要选的教学班！」，且**必须带 `?gnmkdm=N253512`**（漏了返回 HTTP 911） | 不作为数据源，列表一律由 `PartDisplay` 触发 |

**排查方法留档**：盲试参数无解时，用 Playwright 驱动真实浏览器登录并触发一次查询，
把页面实际发出的请求抓下来逐参数对比，是定位这类加密参数的可靠办法——本次正是这样找到
`xkkz_xh`，以及索引页里紧跟在 `firstXkkzId` 之后的 `firstXkkzXh` 隐藏域。

---

## 面板登录鉴权

面板默认启用登录页，未登录时页面跳转 `/login`、接口返回 401。

- 会话 Cookie 是 HMAC-SHA256 签名令牌，有效期 **7 天**，`HttpOnly` + `SameSite=Lax`
- 同 IP 连续 **5 次**失败锁定 **60 秒**（锁定期内正确口令也拒绝）
- 口令用定时安全比较，防时序侧信道；`next` 参数只允许站内跳转，防开放重定向
- 签名密钥持久化在 `<DATA_DIR>/.panel_secret`，**重启服务不会踢下线**；改口令则所有旧会话立即失效

凭据来源：`PANEL_USER` / `PANEL_PASS` → `<DATA_DIR>/.panel_pass`。
未设置 `PANEL_PASS` 时首次启动会生成随机口令，写入 `<DATA_DIR>/.panel_pass`（0600）并在日志打印一次。
**源码中不内置任何默认口令**，避免仓库公开后泄露。

> ⚠️ 面板是**明文 HTTP**，口令与 Cookie 在链路上不加密。
> 要暴露到公网请自行套一层 HTTPS（如 Caddy 反代或 Cloudflare 隧道）。

---

## 自行编译

```bash
cd go-panel

# Linux（静态）
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o jin-grabber ./cmd/grabber

# Windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o jin-grabber.exe ./cmd/grabber
```

国内网络可用 `GOPROXY=https://goproxy.cn,direct`。

跑测试：

```bash
go test ./client/... ./pkg/...
```

---

## 常见问题

**Q：日志报「加密串错误，可以清除浏览器缓存后刷新网页重试！」**
选课轮次凭据（`xkkz_id` / `xkkz_xh`）失效或没取到。程序每次运行都会重新拉取选课页，
所以持续出现通常是**选课轮次已结束或尚未开放**。去教务系统确认当前是否在选课时间内。

**Q：报「当前不属于选课阶段」**
教务系统确实不在选课阶段，或该生本次没有可选轮次。属正常现象。

**Q：点更新课程库报「自主选课与任务落实均失败」**
两条路都不可用：自主选课拉取失败（原因见上），任务落实接口回「并未开放」。
**先把选课时间窗口确认清楚**，窗口内一般只有一条会失败。

**Q：课程库下拉里余量显示「有余量」而不是数字**
本站教务系统不返回容量字段，无法给出数字。见上文「余量是怎么判定的」。

**Q：日志里「上课时间」是空的**
自主选课接口不返回上课时间字段。不影响选课与蹲课（走的是 `jxb_id` / `kch_id`）。

**Q：忘记面板口令**
若用 `PANEL_PASS` 环境变量配置的，改 `/etc/jin-grabber.env` 后重启；
若是自动生成的，查看 `<DATA_DIR>/.panel_pass`。

**Q：想换一台机器迁移**
拷走整个 `DATA_DIR`（含账号、课程库、Cookie 与密钥），新机器设同样的 `DATA_DIR` 即可。

---

## ⚠️ 注意事项与声明

1. **合理使用**：蹲课轮询间隔建议 5 秒以上，避免对教务系统造成过大负载；
2. **账号保护**：`DATA_DIR` 里含**学号与密码明文**，切勿上传到公开平台或分享给他人；
3. **明文传输**：面板是 HTTP，公网暴露前请自行加 HTTPS；
4. **选课周期**：正方系统在未开放时会提示「当前不属于选课阶段」或「任务落实查询并未开放」，
   这是正常现象，选课开放后即可正常拉取与提交；
5. **风险自负**：本工具仅用于减少手动操作，使用前请确认符合学校相关规定。
