package k8s

import (
	"os/exec"
	"reflect"
	"runtime"
	"syscall"
)

// startChild starts a kubectl that outlives the call that starts it, such as
// port-forward or get --watch, and asks the system to kill it when dbfwd dies
// without Close, such as by SIGKILL (D17). Call release once cmd.Wait
// returns.
//
// The system sends that signal when the thread that started the child exits,
// not the process. So the child starts on a thread that its goroutine locks,
// and the goroutine holds the thread until release. The goroutine never
// unlocks it, so the thread exits after the child, and no other goroutine runs
// on it.
func startChild(cmd *exec.Cmd) (release func(), err error) {
	setDeathSignal(cmd)
	started := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		<-done
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return func() { close(done) }, nil
}

// setDeathSignal sets the signal that the system sends the child when its
// parent dies. Only Linux and FreeBSD have the field Pdeathsig, and a build
// constraint on a system is not allowed (AGENTS.md), so it is set by name. On
// another system it does nothing.
func setDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	f := reflect.ValueOf(cmd.SysProcAttr).Elem().FieldByName("Pdeathsig")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int {
		f.SetInt(int64(syscall.SIGKILL))
	}
}
