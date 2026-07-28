//go:build !windows

package antigravity

// readCredentialBlob reports that no keyring reader exists for this platform.
// macOS Keychain and the Linux Secret Service need their own readers; until
// then the adapter degrades to unsupported_platform instead of guessing.
func readCredentialBlob() ([]byte, error) {
	return nil, errUnsupportedPlatform
}
