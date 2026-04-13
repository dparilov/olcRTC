// Package fulltunnel defines the Windows full-tunnel lifecycle scaffold.
//
// This package is intentionally separate from the existing SOCKS-based client
// runtime. It now includes a Windows-only Wintun probe plus adapter open/create
// and close scaffolding plus a dry-run route/DNS runner with rollback
// sequencing, execution status, and error propagation. Live Windows route
// mutation, previous-state capture, address assignment, and packet transport
// remain explicitly unfinished.
package fulltunnel
