//! Windows Job Object：把 sidecar 子进程纳入壳的作业对象。
//!
//! KillOnJobClose 语义：壳进程无论正常退出还是崩溃，句柄关闭即整树
//! 终止——壳永远不会遗留孤儿 web。与 Go 侧沙箱（internal/sandbox）
//! 的作业对象同款机制。

use std::mem;

use windows::Win32::Foundation::{CloseHandle, HANDLE};
use windows::Win32::System::JobObjects::{
    AssignProcessToJobObject, CreateJobObjectW, SetInformationJobObject,
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectExtendedLimitInformation,
    JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
};
use windows::Win32::System::Threading::{OpenProcess, PROCESS_SET_QUOTA, PROCESS_TERMINATE};

pub struct Job(HANDLE);

// Job 句柄跨线程使用（supervisor 线程创建、退出时随进程回收）。
unsafe impl Send for Job {}
unsafe impl Sync for Job {}

impl Job {
    /// 创建带 KillOnJobClose 限制作业对象；失败返回 None（降级为无托管，
    /// 只失去"壳崩溃不遗孤"保证，不影响功能）。
    pub fn create() -> Option<Job> {
        unsafe {
            let h = CreateJobObjectW(None, None).ok()?;
            let mut info = JOBOBJECT_EXTENDED_LIMIT_INFORMATION::default();
            info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            let ok = SetInformationJobObject(
                h,
                JobObjectExtendedLimitInformation,
                &info as *const _ as *const core::ffi::c_void,
                mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
            )
            .is_ok();
            if ok {
                Some(Job(h))
            } else {
                let _ = CloseHandle(h);
                None
            }
        }
    }

    /// 把已启动的子进程（按 pid）纳入作业对象。
    pub fn assign_pid(&self, pid: u32) -> bool {
        unsafe {
            let ph = match OpenProcess(PROCESS_SET_QUOTA | PROCESS_TERMINATE, false, pid) {
                Ok(h) => h,
                Err(_) => return false,
            };
            let ok = AssignProcessToJobObject(self.0, ph).is_ok();
            let _ = CloseHandle(ph);
            ok
        }
    }
}

impl Drop for Job {
    fn drop(&mut self) {
        // 关句柄即触发 KillOnJobClose——无需显式 TerminateJobObject。
        unsafe {
            let _ = CloseHandle(self.0);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_job_succeeds() {
        // 当前进程可创建作业对象（Windows 基础能力）。
        let job = Job::create();
        assert!(job.is_some());
    }

    #[test]
    fn assign_child_process_is_accepted() {
        // 用一次性子进程验证 assign：绝不能把测试进程自己纳入——
        // KillOnJobClose 会在用例结束关句柄时把测试进程一起终止。
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        let mut child = std::process::Command::new("cmd")
            .args(["/c", "ping", "-n", "30", "127.0.0.1"])
            .creation_flags(CREATE_NO_WINDOW)
            .spawn()
            .expect("spawn sleeper");
        let job = Job::create().expect("job");
        assert!(job.assign_pid(child.id()));
        let _ = child.kill();
        let _ = child.wait();
    }
}
