//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	stateKey          = `SOFTWARE\Betty`
	publishedName     = "AirPodsDriverPublishedName"
	spostPath         = 1
	suoiForceDelete   = 0x00000001
	driverPackageGone = syscall.Errno(1168)
)

var (
	setupAPI              = syscall.NewLazyDLL("setupapi.dll")
	newdev                = syscall.NewLazyDLL("newdev.dll")
	setupCopyOEMInfW      = setupAPI.NewProc("SetupCopyOEMInfW")
	setupUninstallOEMInfW = setupAPI.NewProc("SetupUninstallOEMInfW")
	diInstallDriverW      = newdev.NewProc("DiInstallDriverW")
	diUninstallDriverW    = newdev.NewProc("DiUninstallDriverW")
)

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "install" && os.Args[1] != "uninstall") {
		fmt.Fprintln(os.Stderr, "usage: bt-windows-setup <install|uninstall> <BettyAAP.inf>")
		os.Exit(2)
	}
	var err error
	if os.Args[1] == "install" {
		err = install(os.Args[2])
	} else {
		err = uninstall(os.Args[2])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func install(inf string) error {
	absolute, err := resolveINF(inf)
	if err != nil {
		return err
	}
	source, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return err
	}
	destination := make([]uint16, 1024)
	var required uint32
	ok, _, callErr := setupCopyOEMInfW.Call(
		uintptr(unsafe.Pointer(source)), 0, spostPath, 0,
		uintptr(unsafe.Pointer(&destination[0])), uintptr(len(destination)), uintptr(unsafe.Pointer(&required)), 0,
	)
	if ok == 0 {
		return fmt.Errorf("stage AirPods profile driver: %w", callErr)
	}
	fullDestination := syscall.UTF16ToString(destination)
	published := filepath.Base(fullDestination)
	if published == "." || published == "" {
		return fmt.Errorf("Windows did not return the published driver package name")
	}
	var rebootRequired int32
	ok, _, callErr = diInstallDriverW.Call(0, uintptr(unsafe.Pointer(source)), 0, uintptr(unsafe.Pointer(&rebootRequired)))
	if ok == 0 && !isNoMatchingDevice(callErr) {
		_ = removePublished(published)
		return fmt.Errorf("bind AirPods profile driver: %w", callErr)
	}
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, stateKey, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		_ = removePublished(published)
		return fmt.Errorf("record installed driver package: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(publishedName, published); err != nil {
		_ = removePublished(published)
		return err
	}
	return nil
}

func uninstall(inf string) error {
	absolute, err := resolveINF(inf)
	if err != nil {
		return err
	}
	pointer, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return err
	}
	var rebootRequired int32
	if ok, _, callErr := diUninstallDriverW.Call(0, uintptr(unsafe.Pointer(pointer)), 0, uintptr(unsafe.Pointer(&rebootRequired))); ok == 0 && !isGone(callErr) {
		return fmt.Errorf("unbind AirPods profile driver: %w", callErr)
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, stateKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		published, _, valueErr := key.GetStringValue(publishedName)
		key.Close()
		if valueErr == nil && published != "" {
			if err := removePublished(published); err != nil && !isGone(err) {
				return err
			}
		}
	} else if !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, stateKey)
	return nil
}

func resolveINF(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(filepath.Dir(executable), path))
}

func removePublished(name string) error {
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if ok, _, callErr := setupUninstallOEMInfW.Call(uintptr(unsafe.Pointer(pointer)), suoiForceDelete, 0); ok == 0 {
		return fmt.Errorf("remove AirPods profile driver from Driver Store: %w", callErr)
	}
	return nil
}

func isGone(err error) bool {
	return errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) || errors.Is(err, driverPackageGone)
}

func isNoMatchingDevice(err error) bool {
	return errors.Is(err, syscall.Errno(259)) || errors.Is(err, syscall.Errno(433)) || errors.Is(err, syscall.Errno(1168))
}
