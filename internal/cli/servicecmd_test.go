package cli

import (
	"net"
	"os"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/service"
)

// fakeServiceExec 记录全部调用；powershell 查询按任务名返回罐头
// 输出（与其他包的 fake 同构，但 cli 包内自持一份，避免跨包引用
// 测试夹具）。
type fakeServiceExec struct {
	calls   [][]string
	outputs map[string]string
}

func (f *fakeServiceExec) Run(name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	if name == "powershell" {
		joined := strings.Join(args, " ")
		for tn, out := range f.outputs {
			if strings.Contains(joined, tn) {
				return out, nil
			}
		}
		return "NOTFOUND", nil
	}
	return "", nil
}

func (f *fakeServiceExec) count(name string, op string) int {
	n := 0
	for _, c := range f.calls {
		if c[0] == name && len(c) > 1 && c[1] == op {
			n++
		}
	}
	return n
}

func (f *fakeServiceExec) has(args ...string) bool {
	for _, c := range f.calls {
		if len(c) != len(args) {
			continue
		}
		match := true
		for i, a := range args {
			if c[i] != a {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// countOpArg 统计 name 调用中子命令为 op 且参数含 arg 的次数——
// /enable、/disable 是 /change 的尾参，不是独立子命令。
func (f *fakeServiceExec) countOpArg(name string, op string, arg string) int {
	n := 0
	for _, c := range f.calls {
		if c[0] != name || len(c) < 2 || c[1] != op {
			continue
		}
		for _, a := range c[2:] {
			if a == arg {
				n++
				break
			}
		}
	}
	return n
}

// withFakeServiceExec 替换执行器装配钩子，隔离真实 schtasks。
func withFakeServiceExec(t *testing.T) *fakeServiceExec {
	t.Helper()
	fe := &fakeServiceExec{}
	old := newServiceExecer
	newServiceExecer = func() service.Execer { return fe }
	t.Cleanup(func() { newServiceExecer = old })
	return fe
}

func TestServiceInstallStartStatusStopUninstall(t *testing.T) {
	taskTestIdentity(t)
	fe := withFakeServiceExec(t)
	names := []string{"Mindloop-ada-mind", "Mindloop-ada-connector", "Mindloop-ada-web"}

	// install：三个任务注册，回显任务名与日志路径。
	code, out, errText := runCLI(t, "service", "install", "ada")
	if code != 0 {
		t.Fatalf("install exit=%d stderr=%s", code, errText)
	}
	for _, tn := range names {
		if !strings.Contains(out, tn) {
			t.Errorf("install 回显缺 %s:\n%s", tn, out)
		}
	}
	if n := fe.count("schtasks", "/create"); n != 3 {
		t.Fatalf("/create 次数 = %d，要 3", n)
	}

	// start：每个任务 /change /enable + /run。
	code, _, _ = runCLI(t, "service", "start", "ada")
	if code != 0 {
		t.Fatalf("start exit=%d", code)
	}
	if n := fe.countOpArg("schtasks", "/change", "/enable"); n != 3 {
		t.Fatalf("/enable 次数 = %d，要 3", n)
	}
	if n := fe.count("schtasks", "/run"); n != 3 {
		t.Fatalf("/run 次数 = %d，要 3", n)
	}

	// status：PowerShell 罐头输出 → 中文投影；mind 未运行给出提示。
	fe.outputs = map[string]string{
		"Mindloop-ada-mind":      `{"state":"Running","last":"267009","lastRun":"2026/9/27 19:00:00"}`,
		"Mindloop-ada-connector": `{"state":"Ready","last":"0","lastRun":"2026/9/27 18:00:00"}`,
		// web 无罐头 → NOTFOUND → 未注册。
	}
	code, out, _ = runCLI(t, "service", "status", "ada")
	if code != 0 {
		t.Fatalf("status exit=%d", code)
	}
	for _, want := range []string{"运行中", "就绪", "未注册", "267009", "心智进程不在"} {
		if !strings.Contains(out, want) {
			t.Errorf("status 输出缺 %q:\n%s", want, out)
		}
	}

	// stop：优雅停机先行，禁用收尾；stop 阶段绝不新增 /enable。
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	beforeStop := len(fe.calls)
	code, out, _ = runCLI(t, "service", "stop", "ada")
	if code != 0 {
		t.Fatalf("stop exit=%d", code)
	}
	if !strings.Contains(out, "刻意停止") {
		t.Errorf("stop 回显缺刻意停止语义:\n%s", out)
	}
	if !fe.has(exe, "mind", "stop", "ada") {
		t.Error("stop 必须先走 mind stop 优雅停机")
	}
	for _, c := range fe.calls[beforeStop:] {
		if c[0] != "schtasks" {
			continue
		}
		for _, a := range c {
			if a == "/enable" {
				t.Fatal("stop 阶段出现 /enable（刻意停止被复活）")
			}
		}
	}
	if n := fe.countOpArg("schtasks", "/change", "/disable"); n != 3 {
		t.Fatalf("/disable 次数 = %d，要 3", n)
	}

	// uninstall：清掉罐头让全部查询 NOTFOUND → 幂等安静返回。
	fe.outputs = nil
	code, _, _ = runCLI(t, "service", "uninstall", "ada")
	if code != 0 {
		t.Fatalf("uninstall exit=%d", code)
	}
	if n := fe.count("schtasks", "/delete"); n != 0 {
		t.Fatalf("不存在的任务触发了 /delete（%d 次）", n)
	}
}

func TestServiceUninstallDeletesExistingTasks(t *testing.T) {
	taskTestIdentity(t)
	fe := withFakeServiceExec(t)
	fe.outputs = map[string]string{
		"Mindloop-ada-mind":      `{"state":"Disabled","last":"0","lastRun":""}`,
		"Mindloop-ada-connector": `{"state":"Running","last":"267009","lastRun":"2026/9/27 19:00:00"}`,
		// web 缺失 → 只删两个。
	}
	code, _, _ := runCLI(t, "service", "uninstall", "ada")
	if code != 0 {
		t.Fatal("uninstall exit != 0")
	}
	if n := fe.count("schtasks", "/delete"); n != 2 {
		t.Fatalf("/delete 次数 = %d，要 2（只删存在的任务）", n)
	}
	if n := fe.count("schtasks", "/end"); n != 2 {
		t.Fatalf("/end 次数 = %d，要 2", n)
	}
}

func TestServiceUnknownIdentityFails(t *testing.T) {
	newTestHome(t)
	withFakeServiceExec(t)
	code, _, _ := runCLI(t, "service", "install", "ghost")
	if code != 1 {
		t.Fatalf("未知身份 install exit = %d（应 1）", code)
	}
	code, _, _ = runCLI(t, "service", "status")
	if code == 0 {
		t.Fatal("缺身份参数应失败")
	}
}

func TestWarnIfPortBusy(t *testing.T) {
	// 占用端口：必须告警。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var out strings.Builder
	warnIfPortBusy(&out, ln.Addr().String())
	if !strings.Contains(out.String(), "已被占用") {
		t.Fatalf("端口被占用应告警: %q", out.String())
	}

	// 空闲端口：安静。
	out.Reset()
	warnIfPortBusy(&out, "127.0.0.1:0")
	if out.Len() != 0 {
		t.Fatalf("空闲端口不应告警: %q", out.String())
	}
}
