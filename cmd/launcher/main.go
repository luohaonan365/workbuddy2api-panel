// Command launcher 是 WorkBuddy2API 的跨平台启动器：
// 双击（或一条命令）完成「配置/账号自检 → 端口占用处理 → 按需编译 → 启动 → 健康检查 → 打开面板」。
//
// 与平台相关的能力全部收敛在 platform_*.go（build tag 隔离），本文件不出现任何
// 具体系统命令：
//   - 打开浏览器：open / xdg-open / rundll32
//   - 定位端口占用进程：lsof / ss / netstat
//   - 停止进程与等待退出：信号 / taskkill
//   - Go 工具链常见安装位置
//
// 任一步骤缺少系统工具时降级为「打印面板地址」，不阻塞启动。
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// defaultPort 是 config.json 缺失 listen 或 listen 无法解析时的回落端口。
const defaultPort = 7863

// options 是启动器的全部可调项，均可由环境变量提供默认值（见 main）。
type options struct {
	root    string // 运行目录：配置、auths、data、bin 都相对它
	config  string // 配置文件（相对运行目录）
	port    int    // 覆盖监听端口；0 表示读配置
	noOpen  bool   // 不自动打开浏览器
	noPause bool   // 退出后不等待回车
	noBuild bool   // 跳过按需编译
}

var stdin = bufio.NewReader(os.Stdin)

func main() {
	var o options
	flag.StringVar(&o.root, "dir", envOr("WB2API_HOME", ""), "运行目录（默认当前目录）")
	flag.StringVar(&o.config, "config", envOr("WB2API_CONFIG", "config.json"), "配置文件（相对运行目录）")
	flag.IntVar(&o.port, "port", envInt("WB2API_PORT"), "覆盖监听端口（默认读配置 listen）")
	flag.BoolVar(&o.noOpen, "no-open", envBool("WB2API_NO_OPEN"), "不自动打开浏览器")
	flag.BoolVar(&o.noPause, "no-pause", envBool("WB2API_NO_PAUSE"), "退出后不等待回车")
	flag.BoolVar(&o.noBuild, "no-build", false, "跳过按需编译")
	flag.Parse()

	os.Exit(run(o))
}

func run(o options) int {
	root, err := resolveDir(o.root)
	if err != nil {
		fail(err)
		return 1
	}

	cfgPath := o.config
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(root, cfgPath)
	}

	cfg, hasCfg, err := loadConfig(cfgPath)
	if err != nil {
		fail(err)
		return 1
	}
	if !hasCfg {
		if !isSourceDir(root) {
			fail(fmt.Errorf("找不到配置文件：%s（可 cp config.example.json config.json 或指定 -config）", cfgPath))
			return 1
		}
		fmt.Printf("配置文件 %s 不存在：首次启动将自动生成（含随机 api_key，见下方日志）。\n", cfgPath)
	}

	port := portFromListen(cfg.Listen)
	if o.port > 0 {
		port = o.port
	}
	panelURL := fmt.Sprintf("http://127.0.0.1:%d/panel/", port)

	// ---------- 账号目录自检（与旧脚本行为一致）----------
	authDir := cfg.AuthDir
	if !filepath.IsAbs(authDir) {
		authDir = filepath.Join(root, authDir)
	}
	if fi, statErr := os.Stat(authDir); statErr != nil || !fi.IsDir() {
		fail(fmt.Errorf("auth_dir 指向的目录不存在：%s", authDir))
		return 1
	}
	n, err := countJSON(authDir)
	if err != nil {
		fail(err)
		return 1
	}
	fmt.Printf("账号目录：%s（%d 个）\n", authDir, n)
	if n == 0 && !ask("该目录下没有账号文件，面板会是空的。是否继续？(y/N) ", false) {
		fmt.Println("已取消。")
		return 1
	}

	// ---------- 端口占用处理 ----------
	if portBusy(hostFromListen(cfg.Listen), port) && !handlePortBusy(cfg.Listen, port, panelURL, o) {
		return 0
	}

	// ---------- 二进制（缺失或源码更新时编译）----------
	bin, err := resolveBinary(root, o.noBuild)
	if err != nil {
		fail(err)
		return 1
	}

	fmt.Printf("启动：%s\n面板地址：%s\n", bin, panelURL)
	fmt.Println(strings.Repeat("-", 60))

	code, stopped := startAndWait(bin, cfgPath, root, port, panelURL, o)

	fmt.Println(strings.Repeat("-", 60))
	switch {
	case stopped:
		fmt.Println("服务已停止。")
	case code == 0:
		fmt.Println("服务已退出。")
	default:
		fmt.Printf("服务异常退出（退出码 %d）。常见原因：端口被占用 / 配置损坏 / 凭证目录不可写。\n", code)
	}
	if !o.noPause {
		pause()
	}
	return code
}

// handlePortBusy 处理端口已被占用的情况，返回 true 表示可以继续启动。
// 定位不到占用进程、或用户选择保持现状时，只打开面板并返回 false（不重复起进程）。
func handlePortBusy(listen string, port int, panelURL string, o options) bool {
	pid, err := listenerPID(port)
	who := "占用进程未知"
	if err == nil && pid > 0 {
		who = fmt.Sprintf("PID %d", pid)
	}

	if h := healthz(port); h != "" {
		fmt.Printf("端口 %d 已被 %s 占用，服务已在运行：%s\n", port, who, h)
	} else {
		fmt.Printf("端口 %d 已被 %s 占用。\n", port, who)
	}

	if pid <= 0 {
		fmt.Println("无法定位占用进程（缺少 lsof / ss / netstat），直接打开面板。")
		openPanel(panelURL, o.noOpen)
		if !o.noPause {
			pause()
		}
		return false
	}

	if !ask("是否停止它并重启？(y/N) ", false) {
		fmt.Printf("保持现状，直接打开面板：%s\n", panelURL)
		openPanel(panelURL, o.noOpen)
		if !o.noPause {
			pause()
		}
		return false
	}

	if err := stopProcess(pid, false); err != nil {
		fail(fmt.Errorf("停止 PID %d 失败：%w", pid, err))
		return false
	}
	if !waitExit(pid, 10*time.Second) {
		fmt.Println("进程未在 10 秒内退出，强制结束...")
		if err := stopProcess(pid, true); err != nil {
			fail(fmt.Errorf("强制结束 PID %d 失败：%w", pid, err))
			return false
		}
		if !waitExit(pid, 3*time.Second) {
			fail(fmt.Errorf("端口 %d 仍被占用，无法启动", port))
			return false
		}
	}
	for i := 0; i < 20 && portBusy(hostFromListen(listen), port); i++ {
		time.Sleep(250 * time.Millisecond)
	}
	return true
}

// startAndWait 启动网关子进程并守护到退出。stopped 表示退出由 Ctrl+C 触发（正常停止）。
func startAndWait(bin, cfgPath, root string, port int, panelURL string, o options) (code int, stopped bool) {
	cmd := exec.Command(bin, "-config", cfgPath)
	cmd.Dir = root
	// 子进程直接继承控制台：Unix 下与父进程同进程组，Ctrl+C 会同时送达；
	// Windows 下共享 console，同样会收到 CTRL_C_EVENT。
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fail(fmt.Errorf("启动失败：%w", err))
		return 1, false
	}

	if !o.noOpen {
		go func() {
			time.Sleep(2 * time.Second)
			openPanel(panelURL, false)
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, handledSignals()...)
	defer signal.Stop(sigCh)

	var interrupted atomic.Bool
	stopWatch := make(chan struct{})
	defer close(stopWatch)

	pid := cmd.Process.Pid
	go func() {
		select {
		case <-sigCh:
			interrupted.Store(true)
			fmt.Println("\n正在停止服务...")
			_ = stopProcess(pid, false)
			if !waitExit(pid, 10*time.Second) {
				_ = stopProcess(pid, true)
			}
		case <-stopWatch:
		}
	}()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// 健康检查与进程退出竞争：先就绪就打印健康状态，先退出就直接报告。
	health := make(chan string, 1)
	go func() {
		for i := 0; i < 20; i++ {
			time.Sleep(500 * time.Millisecond)
			if h := healthz(port); h != "" {
				health <- h
				return
			}
		}
		health <- ""
	}()

	var waitErr error
	select {
	case h := <-health:
		if h != "" {
			fmt.Println("健康检查：" + h)
		} else {
			fmt.Println("健康检查：10 秒内未就绪，继续观察日志。")
		}
		waitErr = <-exited
	case waitErr = <-exited:
		fmt.Println("服务启动后立即退出。")
	}

	code = 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		} else {
			fmt.Println("等待进程退出失败：", waitErr)
			code = 1
		}
	}
	if interrupted.Load() {
		code = 0
	}
	return code, interrupted.Load()
}

// resolveBinary 依次尝试 bin/wb2api、./wb2api；都没有时在源码目录按需编译。
func resolveBinary(root string, noBuild bool) (string, error) {
	names := []string{filepath.Join("bin", exeName("wb2api")), exeName("wb2api")}
	for _, name := range names {
		p := filepath.Join(root, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		if !noBuild && isSourceDir(root) && sourceNewer(root, fi.ModTime()) {
			fmt.Println("检测到源码有更新，重新编译...")
			if err := buildGateway(root, p); err != nil {
				return "", err
			}
			fmt.Println("编译完成。")
		}
		return p, nil
	}

	if !isSourceDir(root) {
		return "", fmt.Errorf("找不到网关二进制（期望 %s），且当前目录不是源码仓库",
			filepath.Join(root, "bin", exeName("wb2api")))
	}
	if noBuild {
		return "", errors.New("缺少网关二进制，且指定了 -no-build")
	}
	out := filepath.Join(root, "bin", exeName("wb2api"))
	fmt.Println("未找到网关二进制，开始编译（首次约 1-2 分钟）...")
	if err := buildGateway(root, out); err != nil {
		return "", err
	}
	fmt.Println("编译完成。")
	return out, nil
}

// buildGateway 编译 cmd/server；默认代理失败时切 goproxy.cn 重试一次。
func buildGateway(root, out string) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	args := []string{"build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/server"}
	if err := runGo(goBin, root, args, ""); err != nil {
		fmt.Println("编译失败，切换 goproxy.cn 重试...")
		if err := runGo(goBin, root, args, "https://goproxy.cn,direct"); err != nil {
			return fmt.Errorf("编译失败：%w", err)
		}
	}
	return nil
}

func runGo(goBin, dir string, args []string, goproxy string) error {
	c := exec.Command(goBin, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if goproxy != "" {
		c.Env = withEnv("GOPROXY", goproxy)
	}
	return c.Run()
}

// findGo 定位 Go 工具链：WB2API_GO → PATH → 各平台常见安装位置。
func findGo() (string, error) {
	if p := os.Getenv("WB2API_GO"); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	for _, p := range goCandidates() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("找不到 Go 工具链（需 Go 1.22+；也可用 WB2API_GO 指定 go 可执行文件路径）")
}

// sourceNewer 判断源码是否比二进制新，用于决定要不要重编。
func sourceNewer(root string, binTime time.Time) bool {
	skip := map[string]bool{
		".git": true, "bin": true, "auths": true, "data": true,
		"backups": true, "node_modules": true,
	}
	newer := false
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.ModTime().After(binTime) {
			newer = true
			return filepath.SkipAll
		}
		return nil
	})
	return newer
}

func isSourceDir(root string) bool {
	fi, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil && !fi.IsDir()
}

func resolveDir(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("运行目录不存在：%s", abs)
	}
	return abs, nil
}

// gatewayConfig 只取启动器需要的两个字段，其余配置由服务端自行解析。
type gatewayConfig struct {
	Listen  string `json:"listen"`
	AuthDir string `json:"auth_dir"`
}

func loadConfig(path string) (gatewayConfig, bool, error) {
	cfg := gatewayConfig{Listen: ":" + strconv.Itoa(defaultPort), AuthDir: "./auths"}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, false, fmt.Errorf("读取配置失败：%w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, true, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":" + strconv.Itoa(defaultPort)
	}
	if cfg.AuthDir == "" {
		cfg.AuthDir = "./auths"
	}
	return cfg, true, nil
}

// portFromListen 从 ":7863" / "127.0.0.1:7863" / "[::1]:7863" 中取端口。
func portFromListen(listen string) int {
	s := strings.TrimSpace(listen)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 65535 {
		return defaultPort
	}
	return n
}

func countJSON(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			n++
		}
	}
	return n, nil
}

// portBusy 判断端口是否已被占用。
// 不能只靠 bind 探测：macOS/BSD 的 SO_REUSEADDR 允许在 127.0.0.1:port 已被监听时
// 再绑定 0.0.0.0:port，会漏判（实测会直接起进程然后 bind 失败）。
// 因此先尝试连接回环/配置地址，再用 bind 兜住「有监听但不接受连接」的情况。
func portBusy(listenHost string, port int) bool {
	hosts := []string{"127.0.0.1"}
	if h := strings.TrimSpace(listenHost); h != "" && h != "0.0.0.0" && h != "::" && h != "127.0.0.1" {
		hosts = append(hosts, h)
	}
	for _, h := range hosts {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(h, strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			_ = c.Close()
			return true
		}
	}
	bindHost := "0.0.0.0"
	if h := strings.TrimSpace(listenHost); h != "" && h != "::" {
		bindHost = h
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(port)))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// hostFromListen 取监听地址的 host 部分（":7863" → ""，"[::1]:7863" → "::1"）。
func hostFromListen(listen string) string {
	s := strings.TrimSpace(listen)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return strings.Trim(s[:i], "[]")
	}
	return ""
}

func healthz(port int) string {
	cl := http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(b))
}

func openPanel(url string, noOpen bool) {
	if noOpen {
		return
	}
	if err := openBrowser(url); err != nil {
		fmt.Printf("（无法自动打开浏览器：%v，请手动访问）\n", err)
	}
}

// isInteractive 判断是否挂在真实终端上（双击运行时为 true，管道/CI 为 false）。
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ask 在终端里提问；非交互环境直接返回默认值，避免卡住脚本。
func ask(prompt string, def bool) bool {
	if !isInteractive() {
		return def
	}
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return def
	}
	s := strings.ToLower(strings.TrimSpace(line))
	if s == "" {
		return def
	}
	return s == "y" || s == "yes"
}

func pause() {
	if !isInteractive() {
		return
	}
	fmt.Print("按回车键关闭窗口...")
	_, _ = stdin.ReadString('\n')
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "错误：%v\n", err)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func withEnv(key, val string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+val)
}
