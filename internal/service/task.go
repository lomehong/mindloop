// Package service 把三个常驻进程（mind / connector / web）装配成
// Windows 任务计划程序任务：登录自启、崩溃自动重启、刻意停止不被
// 复活——headlong deploy/*.service（每组件一个 systemd unit）的
// Windows 对应物。设计文档：docs/designs/service.md。
//
// 形态决策（设计文档 §2）：不自研监督进程，监督/重启/自启全部委托
// OS 宿主；一组件一任务；InteractiveToken 免管理员以安装者身份
// 运行（实证前提：XML 必须显式带 Principal 与 LogonTrigger.UserId，
// 否则 Access denied）。本包零内部依赖、零第三方依赖，非 Windows
// 上除 XML 渲染外不可用——真实 schtasks 调用由 CLI 层的平台守卫
// 拦截，测试经 Execer 接口注入 fake。
package service

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// writeFile 是 writeTaskXML 的落盘出口（测试替换点）。
var writeFile = func(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

// Component 是被服务化的常驻进程。顺序即安装/启动顺序。
type Component string

const (
	ComponentMind      Component = "mind"
	ComponentConnector Component = "connector"
	ComponentWeb       Component = "web"
)

// Components 是全部受管组件，mind 在前（其余组件不依赖它，但人读
// 状态输出时心智排第一最自然）。
var Components = []Component{ComponentMind, ComponentConnector, ComponentWeb}

// Spec 是安装期固化的绝对路径集。任务绑定 exe 位置：换位置/改名后
// 任务失效（status 可见），重装即修复（设计文档 §3）。
type Spec struct {
	// Identity 是身份名（任务名与命令行参数共用）。
	Identity string
	// Exe 是 mindloop 可执行文件的绝对路径（install 时
	// os.Executable() 固化）。
	Exe string
	// Home 是 MINDLOOP_HOME 绝对路径：任务的 WorkingDirectory 与
	// 日志目录的根。
	Home string
	// User 是任务运行者（安装者账户名，os/user Current().Username）。
	User string
}

// TaskName 是某组件的计划任务名。平铺根文件夹（schtasks 不支持
// /tn 自动建文件夹，实证见设计文档 §2）。
func TaskName(c Component, identity string) string {
	return fmt.Sprintf("Mindloop-%s-%s", identity, c)
}

// LogPath 是组件日志（journald 的对应物：批处理重定向 append）。
func LogPath(spec Spec, c Component) string {
	return winJoin(spec.Home, "logs", string(c)+".log")
}

// winJoin 用反斜杠拼任务内容里的路径。批处理与任务 XML 是给
// Windows 消费的成品，分隔符不能随生成端的 OS 漂移（Linux CI
// 上的 golden 测试钉死的是同一份内容）；文件系统操作仍用
// filepath.Join。
func winJoin(elem ...string) string { return strings.Join(elem, `\`) }

// WrapperPath 是组件的包装批处理：任务动作直接调用它。批处理是
// Windows 原生的 unit 文件——把 MINDLOOP_HOME、exe、参数、日志
// 重定向钉死在一个可读文件里，任务定义不依赖安装会话的环境变量
// （实证教训：计划任务进程看不到安装 shell 的 MINDLOOP_HOME，
// 会漂移到默认 HOME 找不到身份）。
func WrapperPath(spec Spec, c Component) string {
	return winJoin(spec.Home, "run", string(c)+".cmd")
}

// RenderWrapper 生成包装批处理内容。chcp 65001 让后续行按 UTF-8
// 解析（中文路径不乱码；前两行必须保持 ASCII）。% 在 cmd 里是
// 变量展开符，路径含 % 属于畸形路径，不做处理。
func RenderWrapper(spec Spec, c Component) (string, error) {
	if spec.Identity == "" || spec.Exe == "" || spec.Home == "" {
		return "", fmt.Errorf("service: Spec 字段不全（Identity/Exe/Home 必填）")
	}
	args := actionArgs(c, spec.Identity)
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("chcp 65001 >nul\r\n")
	b.WriteString(fmt.Sprintf("rem mindloop %s wrapper（service install 生成；手工改动会被下次 install 覆盖）\r\n", c))
	b.WriteString(fmt.Sprintf("set \"MINDLOOP_HOME=%s\"\r\n", spec.Home))
	b.WriteString(fmt.Sprintf("\"%s\" %s >> \"%s\" 2>&1\r\n", spec.Exe, strings.Join(args, " "), LogPath(spec, c)))
	return b.String(), nil
}

// actionArgs 是组件的启动参数（不含 exe 与日志重定向）。
func actionArgs(c Component, identity string) []string {
	switch c {
	case ComponentMind:
		return []string{"mind", "run", identity}
	case ComponentConnector:
		return []string{"connector", "run-all", identity}
	case ComponentWeb:
		return []string{"web", "--no-build", "--no-open"}
	default:
		panic(fmt.Sprintf("service: 未知组件 %q", c))
	}
}

// RenderXML 渲染一个组件的任务定义。设置逐项对应 headlong unit 的
// 语义（设计文档 §4）：RestartOnFailure 1 分钟×999 是 Restart=
// on-failure + RestartSec=60 的移植（崩溃循环的每次重启都是一轮
// LLM 成本）；ExecutionTimeLimit=PT0S 关掉默认 72h 时限，长驻进程
// 不被宿主腰斩；StartWhenAvailable=false 是"冷启动不重放"的宿主侧
// 同构——错过的登录触发不补跑。
func RenderXML(spec Spec, c Component) (string, error) {
	if spec.Identity == "" || spec.Exe == "" || spec.Home == "" || spec.User == "" {
		return "", fmt.Errorf("service: Spec 字段不全（Identity/Exe/Home/User 必填）")
	}
	// 动作即包装批处理——环境自包含（含 MINDLOOP_HOME），任务
	// 定义不依赖安装会话的 shell 环境（实证教训见 WrapperPath）。
	// 批处理本体由 Install 写盘（WrapperScript × Components）。

	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`)
	b.WriteString("<RegistrationInfo>")
	b.WriteString("<Description>" + xmlEscape(fmt.Sprintf("mindloop %s（身份 %s）", c, spec.Identity)) + "</Description>")
	b.WriteString("<URI>\\" + TaskName(c, spec.Identity) + "</URI>")
	b.WriteString("</RegistrationInfo>")
	b.WriteString("<Triggers>")
	b.WriteString("<LogonTrigger><Enabled>true</Enabled>")
	b.WriteString("<UserId>" + xmlEscape(spec.User) + "</UserId>")
	// 登录风暴缓冲：三个进程错峰 15 秒再起（连接器自带断线重连，
	// 不依赖此延迟，只是不跟桌面启动抢 IO）。
	b.WriteString("<Delay>PT15S</Delay>")
	b.WriteString("</LogonTrigger>")
	b.WriteString("</Triggers>")
	// 实证前提（设计文档 §2）：Principal 缺失 = Access denied。
	b.WriteString("<Principals><Principal id=\"Author\">")
	b.WriteString("<UserId>" + xmlEscape(spec.User) + "</UserId>")
	b.WriteString("<LogonType>InteractiveToken</LogonType>")
	b.WriteString("<RunLevel>LeastPrivilege</RunLevel>")
	b.WriteString("</Principal></Principals>")
	b.WriteString("<Settings>")
	b.WriteString("<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>")
	b.WriteString("<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>")
	b.WriteString("<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>")
	b.WriteString("<AllowHardTerminate>true</AllowHardTerminate>")
	b.WriteString("<StartWhenAvailable>false</StartWhenAvailable>")
	b.WriteString("<RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>")
	b.WriteString("<IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>")
	b.WriteString("<AllowStartOnDemand>true</AllowStartOnDemand>")
	b.WriteString("<Enabled>true</Enabled>")
	b.WriteString("<Hidden>false</Hidden>")
	b.WriteString("<RunOnlyIfIdle>false</RunOnlyIfIdle>")
	b.WriteString("<WakeToRun>false</WakeToRun>")
	b.WriteString("<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>")
	b.WriteString("<Priority>7</Priority>")
	b.WriteString("<RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>")
	b.WriteString("</Settings>")
	b.WriteString("<Actions Context=\"Author\">")
	b.WriteString("<Exec>")
	b.WriteString("<Command>" + xmlEscape(WrapperPath(spec, c)) + "</Command>")
	b.WriteString("<WorkingDirectory>" + xmlEscape(spec.Home) + "</WorkingDirectory>")
	b.WriteString("</Exec>")
	b.WriteString("</Actions>")
	b.WriteString("</Task>")
	return b.String(), nil
}

const xmlHeader = "<?xml version=\"1.0\" encoding=\"UTF-16\"?>"

// xmlEscape 转义 XML 属性/文本的四个实体。schtasks 按声明的
// UTF-16 读文件，writeTaskXML 负责配套编码。
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

// writeTaskXML 把 XML 写成带 BOM 的 UTF-16LE——与 schtasks /export
// 的产物同构，声明与实际编码一致，中文路径/描述不乱码。
func writeTaskXML(path, content string) error {
	encoded := utf16.Encode([]rune(content))
	buf := make([]byte, 2+2*len(encoded))
	buf[0], buf[1] = 0xFF, 0xFE // UTF-16LE BOM
	for i, v := range encoded {
		buf[2+2*i] = byte(v)
		buf[3+2*i] = byte(v >> 8)
	}
	return writeFile(path, buf)
}
