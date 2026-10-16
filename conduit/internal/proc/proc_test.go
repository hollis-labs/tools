//go:build linux

package proc

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestStartTimeIdentifiesAProcess(t *testing.T) {
	st, ok := StartTime(os.Getpid())
	if !ok || st == 0 || !Alive(os.Getpid(), st) {
		t.Fatalf("own pid: %d %v", st, ok)
	}
	if Alive(os.Getpid(), st+1) {
		t.Error("a different start time must not match (pid reuse)")
	}
	if Alive(0, 1) || Alive(-1, 1) || Alive(os.Getpid(), 0) {
		t.Error("invalid identity matched")
	}
}

func TestKillGroupKillsChildrenOfAVerifiedLeader(t *testing.T) {
	cmd := exec.Command("bash", "-c", "sleep 60 & sleep 60; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start, _ := StartTime(pid)
	if !Alive(pid, start) {
		t.Fatal("leader not seen alive")
	}
	time.Sleep(100 * time.Millisecond)
	KillGroup(pid, time.Second)
	_ = cmd.Wait()
	if syscall.Kill(-pid, 0) == nil {
		t.Error("group still has members after KillGroup")
	}
	if Alive(pid, start) {
		t.Error("reaped leader still reported alive")
	}
}

func TestStatParsingSurvivesAnAwkwardCommandName(t *testing.T) {
	// a process whose comm contains spaces and parentheses
	cmd := exec.Command("bash", "-c", "exec -a 'we (ird) name' sleep 5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(100 * time.Millisecond)
	if st, ok := StartTime(cmd.Process.Pid); !ok || st == 0 {
		t.Errorf("could not read start time: %d %v", st, ok)
	}
}
