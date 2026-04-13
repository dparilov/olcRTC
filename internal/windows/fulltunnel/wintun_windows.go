//go:build windows

package fulltunnel

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type wintunDLL struct {
	dll                *windows.LazyDLL
	createAdapterProc  *windows.LazyProc
	openAdapterProc    *windows.LazyProc
	closeAdapterProc   *windows.LazyProc
	getAdapterLUIDProc *windows.LazyProc
}

func ProbeWintun() (WintunProbe, error) {
	probe := WintunProbe{
		Provider: wintunProvider,
		DLL:      wintunDLLName,
		Exports:  append([]string(nil), wintunExportNames...),
	}

	if _, err := loadWintun(); err != nil {
		return probe, err
	}

	probe.Available = true
	return probe, nil
}

func loadWintun() (*wintunDLL, error) {
	dll := windows.NewLazyDLL(wintunDLLName)
	if err := dll.Load(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWintunUnavailable, err)
	}

	createAdapterProc, err := findWintunProc(dll, "WintunCreateAdapter")
	if err != nil {
		return nil, err
	}
	openAdapterProc, err := findWintunProc(dll, "WintunOpenAdapter")
	if err != nil {
		return nil, err
	}
	closeAdapterProc, err := findWintunProc(dll, "WintunCloseAdapter")
	if err != nil {
		return nil, err
	}
	getAdapterLUIDProc, err := findWintunProc(dll, "WintunGetAdapterLUID")
	if err != nil {
		return nil, err
	}

	return &wintunDLL{
		dll:                dll,
		createAdapterProc:  createAdapterProc,
		openAdapterProc:    openAdapterProc,
		closeAdapterProc:   closeAdapterProc,
		getAdapterLUIDProc: getAdapterLUIDProc,
	}, nil
}

func findWintunProc(dll *windows.LazyDLL, name string) (*windows.LazyProc, error) {
	proc := dll.NewProc(name)
	if err := proc.Find(); err != nil {
		return nil, fmt.Errorf("%w: missing %s export in %s: %v", ErrWintunUnavailable, name, wintunDLLName, err)
	}
	return proc, nil
}

func (dll *wintunDLL) openOrCreateAdapter(name, tunnelType string) (uintptr, bool, error) {
	adapter, err := dll.openAdapter(name)
	if err == nil {
		return adapter, true, nil
	}
	if !isWintunAdapterNotFound(err) {
		return 0, false, err
	}

	adapter, err = dll.createAdapter(name, tunnelType)
	if err != nil {
		return 0, false, err
	}
	return adapter, false, nil
}

func (dll *wintunDLL) openAdapter(name string) (uintptr, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("encode adapter name: %w", err)
	}

	adapter, _, callErr := dll.openAdapterProc.Call(uintptr(unsafe.Pointer(namePtr)))
	if adapter == 0 {
		return 0, fmt.Errorf("open Wintun adapter %q: %w", name, normalizeWindowsCallError(callErr))
	}
	return adapter, nil
}

func (dll *wintunDLL) createAdapter(name, tunnelType string) (uintptr, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("encode adapter name: %w", err)
	}
	tunnelTypePtr, err := windows.UTF16PtrFromString(tunnelType)
	if err != nil {
		return 0, fmt.Errorf("encode tunnel type: %w", err)
	}

	adapter, _, callErr := dll.createAdapterProc.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(tunnelTypePtr)),
		0,
	)
	if adapter == 0 {
		return 0, fmt.Errorf("create Wintun adapter %q: %w", name, normalizeWindowsCallError(callErr))
	}
	return adapter, nil
}

func (dll *wintunDLL) closeAdapter(adapter uintptr) {
	dll.closeAdapterProc.Call(adapter)
}

func (dll *wintunDLL) adapterLUID(adapter uintptr) uint64 {
	var luid uint64
	dll.getAdapterLUIDProc.Call(adapter, uintptr(unsafe.Pointer(&luid)))
	return luid
}

func isWintunAdapterNotFound(err error) bool {
	var errno windows.Errno
	return errors.As(err, &errno) && errno == windows.ERROR_FILE_NOT_FOUND
}

func normalizeWindowsCallError(err error) error {
	if err == nil {
		return windows.ERROR_GEN_FAILURE
	}

	var errno windows.Errno
	if errors.As(err, &errno) && errno == windows.ERROR_SUCCESS {
		return windows.ERROR_GEN_FAILURE
	}

	return err
}
