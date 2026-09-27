package schedule

// 本文件把日程的文件访问下沉为包级原语：CLI 与 web 两个消费面
// 必须读写同一组路径、同一套原子写——路径形态与读写语义在这里
// 定义一次（此前在 cli 包，web 面接入时下沉）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Paths 汇总身份日程的三处文件：条目文件在身份目录（用户可编辑，
// 唯一事实），执行日志与状态投影进 <轨迹目录>/run（与运行锁同层）。
func Paths(idDir, tlDir string) (schedulePath, logPath, statePath string) {
	return filepath.Join(idDir, "schedule.json"),
		filepath.Join(tlDir, "run", "schedule.log"),
		filepath.Join(tlDir, "run", "schedule-state.json")
}

// LoadFile 读取条目文件；不存在 = 空表（首次添加不必手建文件），
// 存在但损坏则拒绝——用户的文件不能被我们清掉。
func LoadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("日程文件 %s 解析失败（请先修复，避免覆盖）: %w", path, err)
	}
	return file, nil
}

// SaveFile 原子写（tmp + rename）——心智可能正在热加载该文件，
// 半截写入绝不能被读到。
func SaveFile(path string, file File) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState 读取状态投影；缺失/损坏返回空状态——投影不是事实，
// 列表不因投影不可读而失败。
func LoadState(path string) State {
	var st State
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}
