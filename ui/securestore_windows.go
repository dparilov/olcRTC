//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	kernel32dp         = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree      = kernel32dp.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(data []byte) *dataBlob {
	if len(data) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
}

func (b *dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	return unsafe.Slice(b.pbData, b.cbData)
}

func dpapiEncrypt(data []byte) ([]byte, error) {
	in := newBlob(data)
	var out dataBlob
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(in)), 0, 0, 0, 0,
		0x01, // CRYPTPROTECT_UI_FORBIDDEN
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	enc := make([]byte, out.cbData)
	copy(enc, out.bytes())
	return enc, nil
}

func dpapiDecrypt(data []byte) ([]byte, error) {
	in := newBlob(data)
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(in)), 0, 0, 0, 0,
		0x01, // CRYPTPROTECT_UI_FORBIDDEN
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	dec := make([]byte, out.cbData)
	copy(dec, out.bytes())
	return dec, nil
}

type secretData struct {
	MasterSecret string `json:"master_secret,omitempty"`
	OAuthToken   string `json:"oauth_token,omitempty"`
}

func getSecretsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "secrets.enc"
	}
	return filepath.Join(dir, "olcrtc", "secrets.enc")
}

func saveSecrets(masterSecret, oauthToken string) error {
	data := secretData{MasterSecret: masterSecret, OAuthToken: oauthToken}
	plain, err := json.Marshal(data)
	if err != nil {
		return err
	}
	enc, err := dpapiEncrypt(plain)
	if err != nil {
		return fmt.Errorf("DPAPI encrypt: %w", err)
	}
	path := getSecretsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, enc, 0600)
}

func loadSecrets() (masterSecret, oauthToken string, err error) {
	path := getSecretsPath()
	enc, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil // no secrets stored yet
		}
		return "", "", err
	}
	plain, err := dpapiDecrypt(enc)
	if err != nil {
		return "", "", fmt.Errorf("DPAPI decrypt: %w", err)
	}
	var d secretData
	if err := json.Unmarshal(plain, &d); err != nil {
		return "", "", err
	}
	return d.MasterSecret, d.OAuthToken, nil
}

func deleteSecrets() error {
	return os.Remove(getSecretsPath())
}

func secretStorageType() string {
	return "Windows DPAPI"
}
