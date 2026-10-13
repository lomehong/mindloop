package traj

import "os"

// FileIdentity 返回打开文件的稳定身份（Windows：卷序列号+文件索引；
// POSIX：dev-ino）。派生视图的缓存以它区分"原位重写的文件"与
// "只是追加变大的文件"——身份变化必须触发缓存整体重建，绝不能
// 静默错位（cursor 与任务投影缓存共用这一判据）。
func FileIdentity(f *os.File) string { return fileIdentity(f) }
