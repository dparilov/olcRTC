// Package fulltunnel defines the Windows full-tunnel lifecycle scaffold.
//
// This package is intentionally separate from the existing SOCKS-based client
// runtime. It now includes a Windows-only Wintun probe plus adapter open/create
// and close scaffolding, while route control, address assignment, DNS, and
// packet transport remain explicitly unfinished.
package fulltunnel
