//go:build windows

package antigravity

import (
	"syscall"
	"unsafe"
)

var (
	advapi32      = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW = advapi32.NewProc("CredReadW")
	procCredFree  = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric = 1
	// maxCredentialBlob bounds the copy so a corrupted or oversized credential
	// entry cannot force an unbounded allocation inside the daemon.
	maxCredentialBlob = 1 << 20
)

// credentialW mirrors the Win32 CREDENTIALW layout. Only TargetName,
// CredentialBlobSize, and CredentialBlob are read; the remaining fields exist
// so the struct offsets match what CredReadW writes.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// readCredentialBlob returns a copy of the Antigravity CLI session stored by
// go-keyring in Windows Credential Manager. The read is non-destructive: the
// daemon never writes back, so `agy` stays the sole owner of its auth state.
func readCredentialBlob() ([]byte, error) {
	target, err := syscall.UTF16PtrFromString(keyringTarget)
	if err != nil {
		return nil, errAuthUnavailable
	}
	var credential *credentialW
	result, _, _ := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 || credential == nil {
		return nil, errAuthUnavailable
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))

	size := int(credential.CredentialBlobSize)
	if size <= 0 || size > maxCredentialBlob || credential.CredentialBlob == nil {
		return nil, errAuthUnavailable
	}
	blob := make([]byte, size)
	copy(blob, unsafe.Slice(credential.CredentialBlob, size))
	return blob, nil
}
