package fulltunnel

import "strings"

const (
	wintunProvider          = "wintun"
	defaultWintunTunnelType = "olcRTC"
	wintunDLLName           = "wintun.dll"
)

var wintunExportNames = []string{
	"WintunCreateAdapter",
	"WintunOpenAdapter",
	"WintunCloseAdapter",
	"WintunGetAdapterLUID",
}

// WintunProbe reports whether the local process can resolve the minimal Wintun
// adapter lifecycle surface used by this package.
type WintunProbe struct {
	Provider  string
	DLL       string
	Available bool
	Exports   []string
}

func normalizeAdapterProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return wintunProvider
	}
	return normalized
}
