// Package fulltunnel defines the Windows full-tunnel lifecycle scaffold.
//
// This package is intentionally separate from the existing SOCKS-based client
// runtime. It provides the structure, status model, and backend seams needed
// for future Windows adapter management and route control work without claiming
// that packet transport or OS networking is already implemented.
package fulltunnel
