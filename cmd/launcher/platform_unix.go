//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// exeName macOS/Linux 下二进制没有扩展名。
func exeName(base string) string { return base }

// handledSignals 终端 Ctrl+C 与 kill 都走优雅停止。
func handledSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

// openBrowser macOS 用 open，Linux 依次尝试 xdg-open / gio / sensible-browser。
func openBrowser(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	cands := [][]string{
		{"xdg-open", url},
		{"gio", "open", url},
		{"sensible-browser", url},
	}
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err == nil {
			return exec.Command(c[0], c[1:]...).Start()
		}
	}
	return errors.New("未找到 xdg-open / gio / sensible-browser")
}

// listenerPID 优先 lsof，其次 ss（精简 Linux 镜像可能没有 lsof）。
func listenerPID(port int) (int, error) {
	target := ":" + strconv.Itoa(port)
	if _, err := exec.LookPath("lsof"); err == nil {
		out, err := exec.Command("lsof", "-nP", "-tiTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
		if err == nil {
			for _, f := range strings.Fields(string(out)) {
				if pid, convErr := strconv.Atoi(f); convErr == nil {
					return pid, nil
				}
			}
		}
	}
	if _, err := exec.LookPath("ss"); err == nil {
		out, err := exec.Command("ss", "-ltnp").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.Contains(line, target) {
					continue
				}
				i := strings.Index(line, "pid=")
				if i < 0 {
					continue
				}
				rest := line[i+len("pid="):]
				if j := strings.IndexAny(rest, ","); j >= 0 {
					rest = rest[:j]
				}
				if pid, convErr := strconv.Atoi(strings.TrimSpace(rest)); convErr == nil {
					return pid, nil
				}
			}
		}
	}
	return 0, errors.New("lsof / ss 均不可用")
}

// stopProcess force=false 发 SIGTERM 请求优雅退出，force=true 直接 SIGKILL。
func stopProcess(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}

// waitExit 轮询信号 0 判断进程是否已消失。
func waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// goCandidates 常见安装位置：用户自解压的 ~/goroot、官方 pkg、Homebrew、snap、goenv。
func goCandidates() []string {
	home, _ := os.UserHomeDir()
	list := []string{
		filepath.Join(home, "goroot", "bin", "go"),
		filepath.Join(home, "go", "bin", "go"),
		"/usr/local/go/bin/go",
		"/opt/homebrew/bin/go",
		"/usr/local/bin/go",
		"/snap/bin/go",
	}
	if m, _ := filepath.Glob(filepath.Join(home, "sdk", "go*", "bin", "go")); len(m) > 0 {
		list = append(list, m...)
	}
	return list
}
