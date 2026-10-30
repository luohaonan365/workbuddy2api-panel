//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// exeName Windows 下二进制带 .exe。
func exeName(base string) string { return base + ".exe" }

// handledSignals Windows 控制台只支持 Ctrl+C（CTRL_C_EVENT）。
func handledSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// openBrowser 优先 rundll32（不弹 cmd 窗口），失败退回 start。
func openBrowser(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err == nil {
		return nil
	}
	return exec.Command("cmd", "/c", "start", "", url).Start()
}

// listenerPID 解析 netstat -ano 的 LISTENING 行（末列为 PID）。
func listenerPID(port int) (int, error) {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return 0, err
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		if !strings.HasSuffix(f[1], suffix) {
			continue
		}
		if pid, convErr := strconv.Atoi(f[4]); convErr == nil {
			return pid, nil
		}
	}
	return 0, errors.New("未找到监听该端口的进程")
}

// stopProcess Windows 没有 SIGTERM 语义：taskkill /T 终止进程树，force 时加 /F。
func stopProcess(pid int, force bool) error {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return exec.Command("taskkill", args...).Run()
}

// waitExit 用 tasklist 过滤 PID 判断进程是否已退出。
func waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	target := strconv.Itoa(pid)
	for {
		out, err := exec.Command("tasklist", "/FI", "PID eq "+target, "/NH").Output()
		if err != nil || !strings.Contains(string(out), target) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// goCandidates Windows 常见安装位置：官方安装包、用户级安装、GOPATH。
func goCandidates() []string {
	var list []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		list = append(list, filepath.Join(pf, "Go", "bin", "go.exe"))
	}
	if lad := os.Getenv("LOCALAPPDATA"); lad != "" {
		list = append(list, filepath.Join(lad, "Programs", "Go", "bin", "go.exe"))
	}
	list = append(list, `C:\Go\bin\go.exe`)
	if home, _ := os.UserHomeDir(); home != "" {
		list = append(list, filepath.Join(home, "go", "bin", "go.exe"))
	}
	return list
}
