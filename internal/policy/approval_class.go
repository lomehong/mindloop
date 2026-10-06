package policy

// 需审批类（approval class）——auto（缺省）档下唯一会进入审批等待的
// 操作类别：**删除、外发/发布、提权、凭据、系统改动**五组（2026-10-06
// 与操作员逐组确认）。判定取向与只读白名单（internal/risk）相反：
// 这里是模式黑名单，回答的是"要不要给人过目"——命中多问一次是
// 可接受的代价（错杀一次审批，不放过一次不可逆），所以宁可误报。
//
// 漏判的兜底有两层：tripwire 灾难档（并集并入本类，见下）与快照/undo
// （工作目录内可撤销）。边界如实声明：模式匹配在脚本文本层面，看不进
// 解释器内部的实际行为——heredoc/引号里的文本照扫（注释里提到危险词
// 也会命中，这正是宁误报勿漏报的取向）。增删名单请连同测试一起改。

import "regexp"

// approvalPatterns 五组名单。note 是给操作员读的命中理由。
var approvalPatterns = []riskPattern{
	// —— 删除 / 破坏（不可逆）——
	{regexp.MustCompile(`(?:^|[\s;&|()])(rm|unlink|shred|truncate|rmdir)\s`),
		"包含删除操作（rm/rmdir/shred/truncate 等），可能不可逆"},
	{regexp.MustCompile(`(?i)\b(rimraf|rmtree|rmsync|unlinksync|rmdirsync|removesync|removeall|unlink|rmdir)\s*\(|\bos\.remove\(`),
		"脚本内删除调用（rmtree/rmSync/unlink/RemoveAll 等），可能不可逆"},
	{regexp.MustCompile(`(?i)\bfind\b[^\n]*\s-(exec|execdir|ok|okdir|delete)\b`),
		"find 带执行/删除旗标（-exec/-delete）"},
	{regexp.MustCompile(`\bgit\s+(clean\b|reset\s+--hard\b|checkout\s+--\s)`),
		"丢弃本地变更（git clean / reset --hard / checkout --）"},
	{regexp.MustCompile(`(?i)\b(Remove-Item|del)\s`),
		"Windows 删除（Remove-Item/del）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])dd\s`),
		"按块写入（dd），可能覆盖磁盘/文件"},

	// —— 外发 / 发布 ——
	{regexp.MustCompile(`\bcurl\b[^\n]*\s(-d|-F|-T)\b`),
		"向外部发送数据（curl -d/-F/-T 上传）"},
	{regexp.MustCompile(`\bcurl\b[^\n]*--(data|form|upload-file)`),
		"向外部发送数据（curl --data/--form/--upload-file）"},
	{regexp.MustCompile(`(?i)\bcurl\b[^\n]*\s-X\s*(POST|PUT|PATCH|DELETE)\b`),
		"向外部发送数据（curl -X 写方法）"},
	{regexp.MustCompile(`(?i)\bcurl\b[^\n]*--request\s*=?\s*(POST|PUT|PATCH|DELETE)\b`),
		"向外部发送数据（curl --request 写方法）"},
	{regexp.MustCompile(`(?i)\bwget\b[^\n]*(--post-data|--post-file|--body-data|--method[=\s]+(POST|PUT|DELETE))`),
		"向外部发送数据（wget 上传）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])(ssh|scp|sftp|rsync)\s`),
		"远程连接/文件传输（ssh/scp/sftp/rsync）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])(nc|ncat|telnet|swaks|sendmail|msmtp)\s`),
		"对外网络收发（nc/telnet/sendmail 等）"},
	{regexp.MustCompile(`\bgit\s+push\b`),
		"推送远端仓库（git push）"},
	{regexp.MustCompile(`\bgh\s+(pr|issue|release|gist|repo)\s+(create|edit|delete|close|reopen|merge|comment|upload|rename)\b`),
		"GitHub 写操作（gh 发布/评论类）"},
	{regexp.MustCompile(`(?i)\bgh\s+api\b[^\n]*\s(-X|--method)\s*(POST|PUT|PATCH|DELETE)\b`),
		"GitHub API 写操作（gh api）"},
	{regexp.MustCompile(`(?i)\b(npm|pnpm|yarn)\s+publish\b|\bdocker\s+push\b|\bcargo\s+publish\b|\btwine\s+upload\b|\bgem\s+push\b`),
		"发布到包/镜像仓库（publish/push）"},

	// —— 提权 ——
	{regexp.MustCompile(`(?:^|[\s;&|()])(sudo|doas|gsudo|runas)\s`),
		"请求提权（sudo/runas）"},
	{regexp.MustCompile(`(?i)-Verb\s+RunAs\b`),
		"请求提权（PowerShell RunAs）"},

	// —— 凭据 ——
	{regexp.MustCompile(`(\.ssh\b|id_rsa|\bid_dsa\b|\.aws\b|\.netrc\b|\.git-credentials\b|\.docker[/\\]config\.json)`),
		"可能触及凭据文件（.ssh/.aws/id_rsa/netrc 等）"},
	{regexp.MustCompile(`\.env\b`),
		"可能触及 .env 配置文件（凭据/密钥）"},

	// —— 系统改动 ——
	{regexp.MustCompile(`\bsystemctl\s+(stop|start|restart|disable|enable|mask|kill)\b`),
		"改动系统服务（systemctl）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])service\s+\S+\s+(stop|start|restart)\b`),
		"改动系统服务（service）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])sc\s+(stop|start|delete|config|create)\b`),
		"改动 Windows 服务（sc）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])reg\s+(add|delete|import)\b`),
		"改动注册表（reg add/delete）"},
	{regexp.MustCompile(`\bschtasks\s+/(create|delete|change|run|end)\b`),
		"改动计划任务（schtasks）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])(taskkill|Stop-Process)\b`),
		"终止进程（taskkill/Stop-Process）"},
	{regexp.MustCompile(`(?:^|[\s;&|()])net\s+(stop|start)\b`),
		"改动系统服务（net stop/start）"},
}

// ApprovalClassHits 返回脚本命中的需审批项（人读的理由），含 tripwire
// 灾难档——auto 档据此决定"自动放行"还是"等待批准"。空切片 = 无一命中。
func ApprovalClassHits(script string) []string {
	var out []string
	for _, p := range approvalPatterns {
		if p.re.MatchString(script) {
			out = append(out, p.note)
		}
	}
	return mergeRisks(out, TripwireHits(script))
}
