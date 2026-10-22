// 下载 → 校验 → 暂存 → 替换/安装器：升级的执行半场。所有网络失败
// 与校验失败都显式返回错误——宁可不升级，绝不落半套。
package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Environment 描述执行升级所需的进程事实。
type Environment struct {
	Version   string // 当前版本（cli.Version）
	ExePath   string // os.Executable()（服务化任务绑定的固定路径）
	ShellMode bool   // MINDLOOP_SHELL=1：桌面壳内——走安装器路径
	GOOS      string // 缺省 runtime.GOOS
	GOARCH    string // 缺省 runtime.GOARCH
}

// CurrentEnvironment 从进程事实装配（测试直接手工构造）。
func CurrentEnvironment(version string) Environment {
	exe, _ := os.Executable()
	return Environment{
		Version:   version,
		ExePath:   exe,
		ShellMode: os.Getenv("MINDLOOP_SHELL") == "1",
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
}

// PickAsset 按 GOOS/GOARCH 选 zip 资产（goreleaser 命名约定
// mindloop_{os}_{arch}.zip）。
func PickAsset(assets []Asset, goos, goarch string) *Asset {
	want := "mindloop_" + goos + "_" + goarch + ".zip"
	for i := range assets {
		if strings.EqualFold(assets[i].Name, want) {
			return &assets[i]
		}
	}
	return nil
}

// PickInstaller 选 NSIS 安装包（tauri bundle 追加：mindloop_*_x64-setup.exe）。
func PickInstaller(assets []Asset) *Asset {
	var best *Asset
	for i := range assets {
		n := assets[i].Name
		if strings.HasPrefix(strings.ToLower(n), "mindloop_") &&
			strings.HasSuffix(strings.ToLower(n), "-setup.exe") {
			if best == nil || assets[i].Size > best.Size {
				best = &assets[i]
			}
		}
	}
	return best
}

// PickChecksums 选 checksums.txt。
func PickChecksums(assets []Asset) *Asset {
	for i := range assets {
		if strings.EqualFold(assets[i].Name, "checksums.txt") {
			return &assets[i]
		}
	}
	return nil
}

// Staged 是暂存目录里的升级产物。
type Staged struct {
	Dir      string
	Mindloop string // 解压出的 mindloop 可执行文件
	Robotd   string // 可能为空（旧版 release 未含 robotd）
}

// Download 到 dir 并校验 sha256，解出可执行文件。
func Stage(ctx context.Context, client *http.Client, rel Release, env Environment, dir string) (*Staged, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	zipAsset := PickAsset(rel.Assets, env.GOOS, env.GOARCH)
	if zipAsset == nil {
		return nil, fmt.Errorf("release %s 没有 %s/%s 的 zip 资产", rel.Tag, env.GOOS, env.GOARCH)
	}
	sums := PickChecksums(rel.Assets)
	if sums == nil {
		return nil, errors.New("release 缺 checksums.txt——拒绝无校验的升级")
	}
	zipPath := filepath.Join(dir, zipAsset.Name)
	if err := download(ctx, client, zipAsset.URL, zipPath); err != nil {
		return nil, fmt.Errorf("下载 %s: %w", zipAsset.Name, err)
	}
	sumsPath := filepath.Join(dir, sums.Name)
	if err := download(ctx, client, sums.URL, sumsPath); err != nil {
		return nil, fmt.Errorf("下载 checksums.txt: %w", err)
	}
	if err := verifySHA256(zipPath, sumsPath); err != nil {
		return nil, fmt.Errorf("校验失败: %w", err)
	}
	staged := &Staged{Dir: dir}
	if err := unzipExes(zipPath, dir, &staged.Mindloop, &staged.Robotd); err != nil {
		return nil, err
	}
	if staged.Mindloop == "" {
		return nil, errors.New("zip 里没有 mindloop 可执行文件")
	}
	return staged, nil
}

// ReplaceBinary 就地替换：改名舞步（Windows 允许改名运行中的 exe，
// 不允许覆盖写）。服务化任务绑定的是路径——替换后无需重跑 install。
// 失败自动回滚。
func ReplaceBinary(livePath, staged string) error {
	old := livePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(livePath, old); err != nil {
		return fmt.Errorf("改名现行二进制: %w", err)
	}
	if err := copyFile(staged, livePath); err != nil {
		if rb := os.Rename(old, livePath); rb != nil {
			return fmt.Errorf("写入新二进制失败且回滚失败（%v）——请从 %s 手动恢复", rb, old)
		}
		return fmt.Errorf("写入新二进制失败（已回滚）: %w", err)
	}
	return nil
}

// LaunchInstaller 启动 NSIS 安装器（脱离本进程生命周期——安装器
// 会接管退出/升级/重启）。仅桌面壳内（Windows）会走到这里。
func LaunchInstaller(installerPath string) error {
	return startDetached(installerPath)
}

// startDetached 以独立进程启动安装器：本进程（sidecar）随后可能被
// 安装器杀掉，启动链不能挂在自己身上。
func startDetached(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// Install 编排一次完整升级：选资产 → 下载校验暂存 → 按形态生效。
// 调用方（web 端点）必须在操作员显式点击后才会走到这里。
func Install(ctx context.Context, client *http.Client, rel Release, env Environment) (InstallResult, error) {
	dir, err := os.MkdirTemp("", "mindloop-update-")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(dir)
	staged, err := Stage(ctx, client, rel, env, dir)
	if err != nil {
		return InstallResult{}, err
	}
	if env.ShellMode {
		// 桌面壳内：装机器接管。sidecar 的 exe 由壳持有，绝不自替换。
		inst := PickInstaller(rel.Assets)
		if inst == nil {
			return InstallResult{}, errors.New("release 缺 NSIS 安装包资产")
		}
		installer := filepath.Join(dir, inst.Name)
		if err := download(ctx, client, inst.URL, installer); err != nil {
			return InstallResult{}, fmt.Errorf("下载安装包: %w", err)
		}
		if err := LaunchInstaller(installer); err != nil {
			return InstallResult{}, fmt.Errorf("启动安装器: %w", err)
		}
		return InstallResult{
			Mode:   "installer",
			Detail: "安装器已启动：按指引完成安装（期间会退出应用，装完重启即新版）",
		}, nil
	}
	if env.ExePath == "" {
		return InstallResult{}, errors.New("无法定位现行二进制（os.Executable 失败）")
	}
	var replaced []string
	if err := ReplaceBinary(env.ExePath, staged.Mindloop); err != nil {
		return InstallResult{}, err
	}
	replaced = append(replaced, "mindloop")
	detail := "新二进制已就位 · 重启生效（服务化：mindloop service restart <身份>；手动运行：直接重启）"
	if staged.Robotd != "" {
		robotdPath := filepath.Join(filepath.Dir(env.ExePath), "robotd.exe")
		if _, err := os.Stat(robotdPath); err == nil {
			if err := ReplaceBinary(robotdPath, staged.Robotd); err != nil {
				return InstallResult{Mode: "binary", Replaced: replaced}, fmt.Errorf("mindloop 已替换，robotd 替换失败: %w", err)
			}
			replaced = append(replaced, "robotd")
			detail += "；robotd 已替换（运行中的旧实例在下次拉起时生效）"
		}
	}
	return InstallResult{Mode: "binary", Replaced: replaced, Detail: detail}, nil
}

// InstallResult 是安装端点的响应契约。
type InstallResult struct {
	Mode     string   `json:"mode"` // "installer" | "binary"
	Replaced []string `json:"replaced,omitempty"`
	Detail   string   `json:"detail"`
}

// ---- 内部零件 ---------------------------------------------------------------

func download(ctx context.Context, client *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mindloop-update")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// verifySHA256 按 goreleaser checksums.txt（"HEX  filename" 两空格）
// 校验文件。
func verifySHA256(path, sumsPath string) error {
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	want, ok := lookupChecksum(string(data), filepath.Base(path))
	if !ok {
		return fmt.Errorf("checksums.txt 里没有 %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 不匹配（要 %s 得 %s）", want, got)
	}
	return nil
}

func lookupChecksum(sums, name string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		// goreleaser 格式："HEX  name"（两空格；兼容单空格容错）。
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[1], name) {
			return fields[0], true
		}
	}
	return "", false
}

// unzipExes 从 zip 里解出 mindloop / robotd 可执行文件（平铺进 dir；
// zip 内可能带顶层目录）。
func unzipExes(zipPath, dir string, mindloop, robotd *string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		var target *string
		switch {
		case strings.EqualFold(base, "mindloop.exe"):
			target = mindloop
		case strings.EqualFold(base, "robotd.exe"):
			target = robotd
		default:
			continue
		}
		if err := extractEntry(f, filepath.Join(dir, base)); err != nil {
			return err
		}
		*target = filepath.Join(dir, base)
	}
	return nil
}

func extractEntry(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
