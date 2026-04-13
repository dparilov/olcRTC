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

## Current blocker on this Linux host

The current repo state does not produce a Windows `.exe` from this sandboxed Linux host.

Observed blockers:

1. `CGO_ENABLED=0` fails because Fyne's GLFW/OpenGL stack pulls `github.com/go-gl/gl/v2.1/gl`, which has no usable files for the `windows/amd64` target in that mode.
2. `CGO_ENABLED=1` requires a Windows C cross-compiler. On this host, the available `gcc` is not MinGW and fails with the Windows-specific `-mthreads` flag.

The exact working command expected on a properly provisioned Linux host is:

```bash
GOCACHE=$PWD/build/.gocache \
GOOS=windows \
GOARCH=amd64 \
CGO_ENABLED=1 \
CC=x86_64-w64-mingw32-gcc \
CXX=x86_64-w64-mingw32-g++ \
/home/dima/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64/bin/go \
build -o build/windows-export/windows-client.exe ./cmd/windows-client
```

If the cached Go 1.25.0 toolchain is unavailable, replace the final binary with a local `go 1.25.x` installation.
