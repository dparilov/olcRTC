//go:build !windows

package fulltunnel

import (
	"fmt"
	"runtime"
)

func ProbeWintun() (WintunProbe, error) {
	return WintunProbe{
		Provider: wintunProvider,
		DLL:      wintunDLLName,
		Exports:  append([]string(nil), wintunExportNames...),
	}, fmt.Errorf("%w: Wintun probe is unavailable on %s", ErrWintunUnavailable, runtime.GOOS)
}
