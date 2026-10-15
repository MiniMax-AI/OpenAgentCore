package cli

import (
	"os/exec"
	"syscall"
)

// deleteAfterExit removes what the running executable kept open, once this
// process has exited.
func deleteAfterExit(dir string) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /s /c "ping -n 3 127.0.0.1 >nul & rmdir /s /q "` + dir + `""`,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
		HideWindow:    true,
	}
	_ = cmd.Start()
}
