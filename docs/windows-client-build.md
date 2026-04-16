# Windows Client Build Notes

This repo has a Fyne-based Windows desktop entrypoint at `./cmd/windows-client`.

The repeatable export command is:

```bash
./script/build-windows-client.sh
```

That script writes all build outputs under `build/windows-export/`:

- `summary.txt`
- `cgo-disabled.stdout`
- `cgo-disabled.stderr`
- `cgo-enabled.stdout`
- `cgo-enabled.stderr`

## Prerequisites

- Go 1.25+ toolchain
- MinGW cross-compiler (`x86_64-w64-mingw32-gcc`, `x86_64-w64-mingw32-g++`)
- Linux host (tested on Ubuntu)

## Cross-Compilation Command

```bash
GOCACHE=$PWD/build/.gocache \
GOOS=windows \
GOARCH=amd64 \
CGO_ENABLED=1 \
CC=x86_64-w64-mingw32-gcc \
CXX=x86_64-w64-mingw32-g++ \
go build -o build/windows-export/windows-client.exe ./cmd/windows-client
```

## Known Blockers

1. `CGO_ENABLED=0` fails because Fyne's GLFW/OpenGL stack pulls `github.com/go-gl/gl/v2.1/gl`, which has no usable files for the `windows/amd64` target in that mode.
2. `CGO_ENABLED=1` requires a Windows C cross-compiler. A standard `gcc` (non-MinGW) will fail with the Windows-specific `-mthreads` flag.

## Secret Handling

Secrets (`OLCRTC_MASTER_SECRET`, `OLCRTC_OAUTH_TOKEN`, `OLCRTC_KEY`) are passed
to the subprocess via environment variables, never via command-line arguments.
See `SECURITY.md` for details.
