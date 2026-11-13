//go:build windows

package nativeproc

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func Start(c *exec.Cmd) (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	ready := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ready) }) }
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if c.Cancel != nil {
		c.Cancel = func() error { <-ready; return windows.TerminateJobObject(job, 1) }
	}
	if e = c.Start(); e != nil {
		release()
		windows.CloseHandle(job)
		return nil, e
	}
	fail := func(e error) (func(), error) {
		release()
		c.Process.Kill()
		c.Wait()
		windows.CloseHandle(job)
		return nil, e
	}
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if e != nil {
		return fail(e)
	}
	e = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if e != nil {
		return fail(e)
	}
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if e != nil {
		return fail(e)
	}
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	e = windows.Thread32First(snapshot, &entry)
	found := false
	for e == nil {
		if entry.OwnerProcessID == uint32(c.Process.Pid) {
			thread, te := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if te != nil {
				e = te
				break
			}
			_, e = windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			found = e == nil
			break
		}
		e = windows.Thread32Next(snapshot, &entry)
	}
	windows.CloseHandle(snapshot)
	if !found {
		if e == nil {
			e = windows.ERROR_NOT_FOUND
		}
		return fail(e)
	}
	release()
	return func() { windows.CloseHandle(job) }, nil
}
func PrivateEnvironment(command, dir string) []string {
	system, _ := windows.GetSystemDirectory()
	root, _ := windows.GetWindowsDirectory()
	return []string{"SystemRoot=" + root, "WINDIR=" + root, "PATH=" + filepath.Dir(command) + ";" + system, "TEMP=" + dir, "TMP=" + dir, "HOME=" + dir, "USERPROFILE=" + dir}
}
