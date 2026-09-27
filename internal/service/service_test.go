package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSpec() Spec {
	return Spec{
		Identity: "ada",
		Exe:      `C:\Program Files\mindloop\mindloop.exe`,
		Home:     `C:\Users\lome\.mindloop`,
		User:     "lome",
	}
}

// —— XML 渲染 ——

func TestRenderXMLGoldenMind(t *testing.T) {
	xml, err := RenderXML(testSpec(), ComponentMind)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`,
		`<RegistrationInfo><Description>mindloop mind（身份 ada）</Description>`,
		`<URI>\Mindloop-ada-mind</URI></RegistrationInfo>`,
		`<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>lome</UserId>`,
		`<Delay>PT15S</Delay></LogonTrigger></Triggers>`,
		`<Principals><Principal id="Author"><UserId>lome</UserId>`,
		`<LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel>`,
		`</Principal></Principals>`,
		`<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`<AllowHardTerminate>true</AllowHardTerminate>`,
		`<StartWhenAvailable>false</StartWhenAvailable>`,
		`<RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>`,
		`<IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>`,
		`<AllowStartOnDemand>true</AllowStartOnDemand><Enabled>true</Enabled>`,
		`<Hidden>false</Hidden><RunOnlyIfIdle>false</RunOnlyIfIdle>`,
		`<WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<Priority>7</Priority>`,
		`<RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>`,
		`</Settings>`,
		`<Actions Context="Author"><Exec>`,
		`<Command>C:\Users\lome\.mindloop\run\mind.cmd</Command>`,
		`<WorkingDirectory>C:\Users\lome\.mindloop</WorkingDirectory>`,
		`</Exec></Actions>`,
		`</Task>`,
	}, "")
	if xml != want {
		t.Fatalf("XML 不匹配：\n got: %s\nwant: %s", xml, want)
	}
}

func TestRenderWrapperComponents(t *testing.T) {
	spec := testSpec()
	for _, tc := range []struct {
		c    Component
		args string
	}{
		{ComponentMind, "mind run ada"},
		{ComponentConnector, "connector run-all ada"},
		{ComponentWeb, "web --no-build --no-open"},
	} {
		wrapper, err := RenderWrapper(spec, tc.c)
		if err != nil {
			t.Fatalf("%s: %v", tc.c, err)
		}
		// 环境自包含：MINDLOOP_HOME 钉死（任务进程看不到安装 shell
		// 的环境——实证教训）；exe/参数/日志重定向各就各位。
		for _, want := range []string{
			"chcp 65001 >nul",
			`set "MINDLOOP_HOME=C:\Users\lome\.mindloop"`,
			`"C:\Program Files\mindloop\mindloop.exe" ` + tc.args,
			`>> "C:\Users\lome\.mindloop\logs\` + string(tc.c) + `.log" 2>&1`,
		} {
			if !strings.Contains(wrapper, want) {
				t.Errorf("%s: 包装脚本缺 %q\n%s", tc.c, want, wrapper)
			}
		}
		if !strings.HasSuffix(wrapper, "\r\n") {
			t.Errorf("%s: 批处理必须 CRLF 结尾", tc.c)
		}
	}
	if _, err := RenderWrapper(Spec{}, ComponentMind); err == nil {
		t.Fatal("空 Spec 应该报错")
	}
}

func TestRenderXMLComponents(t *testing.T) {
	for _, c := range Components {
		xml, err := RenderXML(testSpec(), c)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if !strings.Contains(xml, `\Mindloop-ada-`+string(c)+`</URI>`) {
			t.Errorf("%s: URI 错误", c)
		}
		if !strings.Contains(xml, `<Command>C:\Users\lome\.mindloop\run\`+string(c)+`.cmd</Command>`) {
			t.Errorf("%s: 任务动作应指向包装批处理", c)
		}
	}
}

func TestRenderWrapperHandlesSpecialChars(t *testing.T) {
	spec := testSpec()
	spec.Exe = `C:\a&b\mind loop.exe`
	wrapper, err := RenderWrapper(spec, ComponentMind)
	if err != nil {
		t.Fatal(err)
	}
	// & 在批处理的双引号内原样安全；不做任何 XML 式转义。
	if !strings.Contains(wrapper, `"C:\a&b\mind loop.exe" mind run ada`) {
		t.Errorf("特殊字符路径损坏:\n%s", wrapper)
	}
	// Home 含 & 时 XML 侧仍需转义（Command/WorkingDirectory）。
	spec.Home = `C:\x&y`
	xml, err := RenderXML(spec, ComponentMind)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `C:\x&amp;y\run\mind.cmd`) {
		t.Errorf("XML 内 & 未转义:\n%s", xml)
	}
}

func TestRenderXMLRejectsIncompleteSpec(t *testing.T) {
	if _, err := RenderXML(Spec{}, ComponentMind); err == nil {
		t.Fatal("空 Spec 应该报错")
	}
}

// —— 生命周期 ——

// fakeExec 记录全部调用；powershell 查询按任务名返回罐头输出。
type fakeExec struct {
	calls   [][]string
	outputs map[string]string // 任务名 → powershell 查询输出
}

func (f *fakeExec) Run(name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	if name == "powershell" {
		for tn, out := range f.outputs {
			if strings.Contains(strings.Join(args, " "), tn) {
				return out, nil
			}
		}
		return "NOTFOUND", nil
	}
	return "", nil
}

func (f *fakeExec) has(name string, args ...string) bool {
	for _, c := range f.calls {
		if len(c) != 1+len(args) || c[0] != name {
			continue
		}
		match := true
		for i, a := range args {
			if c[i+1] != a {
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

func (f *fakeExec) count(name string, op string) int {
	n := 0
	for _, c := range f.calls {
		if c[0] == name && len(c) > 1 && c[1] == op {
			n++
		}
	}
	return n
}

func newTestManager(t *testing.T) (*Manager, *fakeExec, string) {
	t.Helper()
	home := t.TempDir()
	m := &Manager{Spec: Spec{Identity: "ada", Exe: `C:\ml\mindloop.exe`, Home: home, User: "lome"}}
	fe := &fakeExec{}
	m.Exec = fe
	return m, fe, home
}

func TestInstallRegistersThreeTasksWithUTF16XML(t *testing.T) {
	m, fe, home := newTestManager(t)
	var capturedXML [][]byte
	var wrapperPaths []string
	oldTemp, oldWrite := tempFile, writeFile
	oldRemove, oldDirs := removeFile, makeDirs
	oldWriteText := writeTextFile
	tempFile = func(pattern string) (string, error) {
		return filepath.Join(home, "task-"+pattern+".tmp"), nil
	}
	writeFile = func(path string, data []byte) error {
		capturedXML = append(capturedXML, data)
		return nil
	}
	writeTextFile = func(path string, data []byte, perm os.FileMode) error {
		// 包装路径是 Windows 内容形态（反斜杠），不能经 filepath.Base
		// 拆分——按后缀断言组件名。
		wrapperPaths = append(wrapperPaths, path[strings.LastIndex(path, `\`)+1:])
		return nil
	}
	removeFile = func(string) {}
	var madeDirs []string
	makeDirs = func(path string) error { madeDirs = append(madeDirs, path); return nil }
	defer func() {
		tempFile, writeFile, removeFile, makeDirs = oldTemp, oldWrite, oldRemove, oldDirs
		writeTextFile = oldWriteText
	}()

	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if n := fe.count("schtasks", "/create"); n != 3 {
		t.Fatalf("schtasks /create 次数 = %d，要 3", n)
	}
	if len(capturedXML) != 3 {
		t.Fatalf("写入的 XML 文件数 = %d，要 3", len(capturedXML))
	}
	for _, data := range capturedXML {
		if len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE {
			t.Fatal("任务 XML 必须是带 BOM 的 UTF-16LE")
		}
	}
	if len(wrapperPaths) != 3 {
		t.Fatalf("包装批处理写入数 = %d，要 3", len(wrapperPaths))
	}
	for _, c := range Components {
		if !slicesContain(wrapperPaths, string(c)+".cmd") {
			t.Errorf("缺包装批处理 %s.cmd", c)
		}
	}
	wantDirs := map[string]bool{
		filepath.Join(home, "logs"): false,
		filepath.Join(home, "run"):  false,
	}
	for _, d := range madeDirs {
		if _, ok := wantDirs[d]; !ok {
			t.Fatalf("意外建目录 %q", d)
		}
		wantDirs[d] = true
	}
	for d, seen := range wantDirs {
		if !seen {
			t.Fatalf("目录未建立: %q", d)
		}
	}
	if !fe.has("schtasks", "/create", "/tn", "Mindloop-ada-mind", "/xml", filepath.Join(home, "task-mindloop-task-*.xml.tmp"), "/f") {
		t.Errorf("mind 任务注册调用形态不符: %v", fe.calls)
	}
}

func slicesContain(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func TestStartEnablesThenRuns(t *testing.T) {
	m, fe, _ := newTestManager(t)
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	// 顺序断言：每个组件先 /enable 再 /run。
	var ops []string
	for _, c := range fe.callsSlice() {
		if c[0] == "schtasks" {
			ops = append(ops, c[1])
		}
	}
	want := []string{"/change", "/run", "/change", "/run", "/change", "/run"}
	if strings.Join(ops, ",") != strings.Join(want, ",") {
		t.Fatalf("启动顺序 = %v，要 %v", ops, want)
	}
	if !fe.has("schtasks", "/run", "/tn", "Mindloop-ada-web") {
		t.Error("缺 web 的 /run")
	}
}

func TestStopGracefulMindThenEndDisable(t *testing.T) {
	m, fe, _ := newTestManager(t)
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if !fe.has(`C:\ml\mindloop.exe`, "mind", "stop", "ada") {
		t.Error("stop 必须先走 mind stop 优雅停机")
	}
	if n := fe.count("schtasks", "/end"); n != 3 {
		t.Fatalf("/end 次数 = %d，要 3", n)
	}
	if n := fe.count("schtasks", "/change"); n != 3 {
		t.Fatalf("/change（/disable）次数 = %d，要 3", n)
	}
	// 刻意停止标记：最后一个动作是 /disable，绝不能有 /enable。
	for _, c := range fe.callsSlice() {
		if c[0] == "schtasks" && c[1] == "/enable" {
			t.Fatal("stop 路径不允许出现 /enable")
		}
	}
}

func TestUninstallIdempotent(t *testing.T) {
	m, fe, _ := newTestManager(t)
	// 全部任务不存在：安静返回，无 /delete。
	fe.outputs = map[string]string{}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if n := fe.count("schtasks", "/delete"); n != 0 {
		t.Fatalf("不存在的任务不该触发 /delete（%d 次）", n)
	}

	// 任务存在：/end + /delete。
	fe2 := &fakeExec{outputs: map[string]string{
		"Mindloop-ada-mind":      `{"state":"Ready","last":"0","lastRun":""}`,
		"Mindloop-ada-connector": `{"state":"Running","last":"267009","lastRun":"2026/9/27 19:00:00"}`,
	}}
	m2 := &Manager{Spec: m.Spec, Exec: fe2}
	if err := m2.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if n := fe2.count("schtasks", "/delete"); n != 2 {
		t.Fatalf("/delete 次数 = %d，要 2（只删存在的任务）", n)
	}
	if n := fe2.count("schtasks", "/end"); n != 2 {
		t.Fatalf("/end 次数 = %d，要 2", n)
	}
}

func TestStatusParsesPowerShellOutput(t *testing.T) {
	m, fe, _ := newTestManager(t)
	fe.outputs = map[string]string{
		"Mindloop-ada-mind":      `{"state":"Running","last":"267009","lastRun":"2026/9/27 19:00:00"}`,
		"Mindloop-ada-connector": `{"state":"Ready","last":"0","lastRun":"2026/9/27 18:00:00"}`,
		// web 缺失 → NOTFOUND 兜底。
	}
	st, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 {
		t.Fatalf("状态条目数 = %d，要 3", len(st))
	}
	if !st[0].Exists || st[0].State != "Running" || st[0].LastResult != "267009" {
		t.Errorf("mind 状态解析错误: %+v", st[0])
	}
	if st[2].Exists {
		t.Errorf("web 不存在时 Exists 应为 false: %+v", st[2])
	}
	if st[2].Name != "Mindloop-ada-web" {
		t.Errorf("名字缺失: %+v", st[2])
	}
}

func TestStatusPropagatesPowershellFailure(t *testing.T) {
	m, _, _ := newTestManager(t)
	m.Exec = failingExec{}
	if _, err := m.Status(); err == nil {
		t.Fatal("powershell 失败应传播")
	}
}

type failingExec struct{}

func (failingExec) Run(name string, args ...string) (string, error) {
	return "", errors.New("boom")
}

// —— 小工具 ——

// callsSlice 是 fakeExec.calls 的别名视图（可读性）。
func (f *fakeExec) callsSlice() [][]string { return f.calls }

func TestTaskNameAndLogPath(t *testing.T) {
	spec := testSpec()
	if got := TaskName(ComponentConnector, "ada"); got != "Mindloop-ada-connector" {
		t.Errorf("TaskName = %q", got)
	}
	if got := LogPath(spec, ComponentWeb); !strings.HasSuffix(got, `logs\web.log`) {
		t.Errorf("LogPath = %q", got)
	}
	if got := LogDir(spec); !strings.HasSuffix(got, "logs") {
		t.Errorf("LogDir = %q", got)
	}
}
