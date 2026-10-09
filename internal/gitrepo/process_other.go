//go:build !darwin && !linux

package gitrepo

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
