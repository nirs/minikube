//go:build darwin

/*
Copyright 2024 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// package vment provides the helper process connecting virtual machines to the
// vmnet network.
package vmnet

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/state"
)

const (
	pidfileName    = "vment-helper.pid"
	logfileName    = "vment-helper.log"
	executablePath = "/opt/vmnet-helper/bin/vmnet-helper"
)

// Helper manages the vmnet-helper process.
type Helper struct {
	// The minikube machine name this helper is serving.
	MachineName string

	// Minikube home directory.
	StorePath string

	// Set when vmnet interface is started.
	macAddress string
}

type interfaceInfo struct {
	MACAddress string `json:"vmnet_mac_address"`
}

// HelperAvailable tells if vment-helper executable is installed and configured
// correctly.
func HelperAvailable() bool {
	version, err := exec.Command("sudo", "--non-interactive", executablePath, "--version").Output()
	if err != nil {
		log.Debugf("Failed to run vmnet-helper: %w", executablePath, err)
		return false
	}
	log.Debugf("Using vmnet-helper version %q", executablePath, version)
	return true
}

// Start the vmnet-helper child process, creating the vmnet interface for the
// machine. sock is a connected unix datagram socket to pass the helper child
// process.
func (h *Helper) Start(sock *os.File) error {
	cmd := exec.Command(
		"sudo",
		"--non-interactive",
		"--close-from", fmt.Sprintf("%d", sock.Fd()+1),
		executablePath,
		"--fd", fmt.Sprintf("%d", sock.Fd()),
		"--interface-id", uuidFromName("io.k8s.sigs.minikube."+h.MachineName),
	)

	cmd.ExtraFiles = []*os.File{sock}

	// Create vment-helper in a new process group so it is not harmed when
	// terminating the minikube process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	logfile, err := h.openLogfile()
	if err != nil {
		return fmt.Errorf("failed to open helper logfile: %w", err)
	}
	defer logfile.Close()
	cmd.Stderr = logfile

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create helper stdout pipe: %w", err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start vmnet-helper: %w", err)
	}

	log.Infof("Started vmnet-helper (pid=%v)", cmd.Process.Pid)

	if err := writePidfile(h.machinePath(pidfileName), cmd.Process.Pid); err != nil {
		return fmt.Errorf("failed to write vmnet-helper pidfile: %w", err)
	}

	var info interfaceInfo
	if err := json.NewDecoder(stdout).Decode(&info); err != nil {
		return fmt.Errorf("failed to decode vmnet interface info: %w", err)
	}

	log.Infof("Got mac address %q", info.MACAddress)
	h.macAddress = info.MACAddress

	return nil
}

// GetMACAddress reutuns the mac address assigned by vment framework.
func (h *Helper) GetMACAddress() string {
	return h.macAddress
}

// Stop the vmnet-helper child process.
func (h *Helper) Stop() error {
	path := h.machinePath(pidfileName)
	pid, err := readPidfile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	log.Infof("Terminate vmnet-helper (pid=%v)", pid)
	// Terminate sudo, which will terminate vmnet-helper.
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if err != os.ErrProcessDone {
			return err
		}
	}
	os.Remove(path)
	return nil
}

// Kill the vmnet-helper child process.
func (h *Helper) Kill() error {
	path := h.machinePath(pidfileName)
	pid, err := readPidfile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	log.Infof("Kill vmnet-helper process group (pgid=%v)", pid)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if err != syscall.ESRCH {
			return err
		}
	}
	os.Remove(path)
	return nil
}

// GetState returns the vment-helper child process state.
func (h *Helper) GetState() (state.State, error) {
	path := h.machinePath(pidfileName)
	pid, err := readPidfile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state.Stopped, nil
		}
		return state.Error, err
	}
	if err := checkPid(pid); err != nil {
		// No pid, remove pidfile
		os.Remove(path)
		return state.Stopped, nil
	}
	return state.Running, nil
}

func (h *Helper) openLogfile() (*os.File, error) {
	path := h.machinePath(logfileName)
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

func (h *Helper) machinePath(fileName string) string {
	return filepath.Join(h.StorePath, "machines", h.MachineName, fileName)
}

func writePidfile(path string, pid int) error {
	data := fmt.Sprintf("%v", pid)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		return err
	}
	return nil
}

func readPidfile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return -1, err
	}
	return pid, nil
}

func checkPid(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.Signal(0))
}

// uuidFromName generated a random UUID (type 4) from string name. This is
// useful for creating interface ID from virtual machine name to ensure the same
// MAC address in all runs.
func uuidFromName(name string) string {
	sum := sha256.Sum256([]byte(name))
	uuid := sum[:16]
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // Version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // Variant is 10
	return fmt.Sprintf("%4x-%2x-%2x-%2x-%6x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// Apple recommend receive buffer size to be 4 times the size of the send buffer
// size, but send buffer size is not used to allocate a buffer in datagram
// sockets, it only limits the maximum packet size. Must be larger than TSO
// packets size (65550 bytes).
const sendBufferSize = 65 * 1024

// The receive buffer size determine how many packets can be queued by the
// peer. Using bigger receive buffer size make ENOBUFS error less likely for the
// peer and improves throughput.
const recvBufferSize = 4 * 1024 * 1024

// Socketpair returns a pair of connected unix datagram sockets that can be used
// to connect the helper and a vm. Pass one socket to the helper child process
// and the other to the vm child process.
func Socketpair() (*os.File, *os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, nil, err
	}
	// Setting buffer size is an optimization - don't fail on errors.
	for _, fd := range fds {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, sendBufferSize)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, recvBufferSize)
	}
	return os.NewFile(uintptr(fds[0]), "sock1"), os.NewFile(uintptr(fds[1]), "sock2"), nil
}
