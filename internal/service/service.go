package service

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Execer 抽象外部命令（schtasks / powershell / mindloop 自身），
// 测试注入 fake 断言调用序列。非零退出返回携带 stderr 的错误。
type Execer interface {
	Run(name string, args ...string) (string, error)
}

// Exec 是 Execer 的生产实现。
type Exec struct{}

// Run 实现 Execer。
func (Exec) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%s: %s", name, msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// Manager 是一个身份的三个计划任务的装配面。全部方法幂等：
// install 覆盖注册（schtasks /f），uninstall 对不存在的任务安静
// 返回，stop 的 /end 对没在跑的任务忽略报错（设计文档 §6）。
type Manager struct {
	Spec Spec
	Exec Execer
}

func (m *Manager) execer() Execer {
	if m.Exec != nil {
		return m.Exec
	}
	return Exec{}
}

// schtasks 运行一条 schtasks 子命令。
func (m *Manager) schtasks(op string, tn string, extra ...string) error {
	args := append([]string{op, "/tn", tn}, extra...)
	_, err := m.execer().Run("schtasks", args...)
	return err
}

// Install 逐组件渲染任务 XML 并覆盖注册。logs 与 run 目录先行建立
// （批处理的重定向目标必须存在，cmd 的 >> 不会自建目录）；每组件
// 的包装批处理先落盘——任务动作即批处理，环境自包含。
func (m *Manager) Install() error {
	if err := makeDirs(LogDir(m.Spec)); err != nil {
		return fmt.Errorf("service: 建日志目录失败: %w", err)
	}
	if err := makeDirs(RunDir(m.Spec)); err != nil {
		return fmt.Errorf("service: 建 run 目录失败: %w", err)
	}
	for _, c := range Components {
		wrapper, err := RenderWrapper(m.Spec, c)
		if err != nil {
			return fmt.Errorf("service %s: %w", c, err)
		}
		if err := writeTextFile(WrapperPath(m.Spec, c), []byte(wrapper), 0o644); err != nil {
			return fmt.Errorf("service %s: 写包装批处理失败: %w", c, err)
		}
		xml, err := RenderXML(m.Spec, c)
		if err != nil {
			return fmt.Errorf("service %s: %w", c, err)
		}
		tmp, err := tempFile("mindloop-task-*.xml")
		if err != nil {
			return fmt.Errorf("service: 建临时文件失败: %w", err)
		}
		err = func() error {
			if err := writeTaskXML(tmp, xml); err != nil {
				return err
			}
			return m.schtasks("/create", TaskName(c, m.Spec.Identity), "/xml", tmp, "/f")
		}()
		removeFile(tmp)
		if err != nil {
			return fmt.Errorf("service: 注册任务 %s 失败: %w", TaskName(c, m.Spec.Identity), err)
		}
	}
	return nil
}

// Start 启用并立即拉起全部任务（/enable 兜住"刻意停止"留下的
// Disabled 状态）。
func (m *Manager) Start() error {
	for _, c := range Components {
		tn := TaskName(c, m.Spec.Identity)
		if err := m.schtasks("/change", tn, "/enable"); err != nil {
			return fmt.Errorf("service: 启用 %s 失败: %w", tn, err)
		}
		if err := m.schtasks("/run", tn); err != nil {
			return fmt.Errorf("service: 拉起 %s 失败: %w", tn, err)
		}
	}
	return nil
}

// Stop 停全部任务。mind 先走优雅停机（停机标志 + 调度器心跳收尾，
// 干净退出码；在途思考不腰斩），失败或超时由 /end 硬杀兜底；
// connector/web 无优雅通道，直接 /end——三组件都按崩溃窗口设计
// （cursor 三态、追加轨迹、Job Object 收树），硬杀是设计内的输入。
// 随后 /disable 全部：**刻意停止标记**——防住 RestartOnFailure 对
// 非零退出的复活，也防住下次登录自启（设计文档 §6）。
func (m *Manager) Stop() error {
	_, _ = m.execer().Run(m.Spec.Exe, "mind", "stop", m.Spec.Identity)
	for _, c := range Components {
		// 任务没在跑时 /end 会报错——刻意停止语义的一部分，忽略。
		_ = m.schtasks("/end", TaskName(c, m.Spec.Identity))
	}
	for _, c := range Components {
		if err := m.schtasks("/change", TaskName(c, m.Spec.Identity), "/disable"); err != nil {
			return fmt.Errorf("service: 停用 %s 失败: %w", TaskName(c, m.Spec.Identity), err)
		}
	}
	return nil
}

// Uninstall 停跑并删除全部任务。任务不存在时安静返回（幂等）。
func (m *Manager) Uninstall() error {
	for _, c := range Components {
		tn := TaskName(c, m.Spec.Identity)
		status, err := m.queryTask(tn)
		if err != nil {
			return fmt.Errorf("service: 查询 %s 失败: %w", tn, err)
		}
		if !status.Exists {
			continue
		}
		_ = m.schtasks("/end", tn)
		if err := m.schtasks("/delete", tn, "/f"); err != nil {
			return fmt.Errorf("service: 删除 %s 失败: %w", tn, err)
		}
	}
	return nil
}

// TaskStatus 是一个计划任务的状态投影。LastResult 保留调度器的
// 原始 HRESULT（0=成功，267009=运行中，267011=从未运行，
// 267014=被终止）；展示层再翻译。
type TaskStatus struct {
	Name       string
	Exists     bool
	State      string
	LastResult string
	LastRun    string
}

// Status 投影全部组件的任务状态。信息源刻意用 PowerShell 的
// Get-ScheduledTask（State 是英文枚举、LastTaskResult 是数字），
// 避开 schtasks /query 的本地化输出——中文系统的"就绪/正在运行"
// 不可解析（设计文档 §6）。
func (m *Manager) Status() ([]TaskStatus, error) {
	out := make([]TaskStatus, 0, len(Components))
	for _, c := range Components {
		s, err := m.queryTask(TaskName(c, m.Spec.Identity))
		if err != nil {
			return nil, err
		}
		s.Name = TaskName(c, m.Spec.Identity)
		out = append(out, s)
	}
	return out, nil
}

// queryTask 用 PowerShell 查单个任务的 locale 无关状态。
func (m *Manager) queryTask(tn string) (TaskStatus, error) {
	s := TaskStatus{Name: tn}
	script := fmt.Sprintf(`$t = Get-ScheduledTask -TaskName '%s' -ErrorAction SilentlyContinue; `+
		`if (-not $t) { 'NOTFOUND'; exit }; `+
		`$i = $t | Get-ScheduledTaskInfo; `+
		`ConvertTo-Json -Compress @{ state = [string]$t.State; last = [string]$i.LastTaskResult; lastRun = [string]$i.LastRunTime }`, tn)
	out, err := m.execer().Run("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return s, fmt.Errorf("service: powershell 查询失败: %w", err)
	}
	if strings.Contains(out, "NOTFOUND") {
		return s, nil
	}
	var payload struct {
		State   string `json:"state"`
		Last    string `json:"last"`
		LastRun string `json:"lastRun"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		return s, fmt.Errorf("service: 解析任务状态失败（%q）: %w", strings.TrimSpace(out), err)
	}
	s.Exists = true
	s.State = payload.State
	s.LastResult = payload.Last
	s.LastRun = payload.LastRun
	return s, nil
}

// LogDir 暴露日志目录（install 预建、CLI 展示用）。
func LogDir(spec Spec) string { return filepath.Join(spec.Home, "logs") }

// RunDir 是包装批处理的目录（install 预建）。
func RunDir(spec Spec) string { return filepath.Join(spec.Home, "run") }

// —— 落盘出口（测试替换点）——

var (
	tempFile = func(pattern string) (string, error) {
		f, err := os.CreateTemp("", pattern)
		if err != nil {
			return "", err
		}
		name := f.Name()
		_ = f.Close()
		return name, nil
	}
	removeFile    = func(path string) { _ = os.Remove(path) }
	makeDirs      = func(path string) error { return os.MkdirAll(path, 0o755) }
	writeTextFile = os.WriteFile
)
