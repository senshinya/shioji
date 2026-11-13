package tailnet

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var ErrUserAction = errors.New("Tailnet 身份需要本机授权，请在这台 Mac 上重新连接")

// The network host uses Security.framework directly. It does not initialize
// MyGo, a renderer, or a GUI application merely to read a Keychain item.
type identityKeychain struct {
	err          error
	constant     map[string]uintptr
	stringCreate func(uintptr, string, uint32) uintptr
	dictCreate   func(uintptr, int, uintptr, uintptr) uintptr
	dictSet      func(uintptr, uintptr, uintptr)
	dataCreate   func(uintptr, []byte, int) uintptr
	dataLength   func(uintptr) int
	dataBytes    func(uintptr) uintptr
	release      func(uintptr)
	copy         func(uintptr, *uintptr) int32
	add          func(uintptr, *uintptr) int32
	interaction  func(bool) int32
	keys, values uintptr
}

var identitySecurity = sync.OnceValue(func() *identityKeychain {
	k := &identityKeychain{constant: map[string]uintptr{}}
	cf, e := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if e != nil {
		k.err = e
		return k
	}
	sec, e := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if e != nil {
		k.err = e
		return k
	}
	purego.RegisterLibFunc(&k.stringCreate, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&k.dictCreate, cf, "CFDictionaryCreateMutable")
	purego.RegisterLibFunc(&k.dictSet, cf, "CFDictionarySetValue")
	purego.RegisterLibFunc(&k.dataCreate, cf, "CFDataCreate")
	purego.RegisterLibFunc(&k.dataLength, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&k.dataBytes, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&k.release, cf, "CFRelease")
	purego.RegisterLibFunc(&k.copy, sec, "SecItemCopyMatching")
	purego.RegisterLibFunc(&k.add, sec, "SecItemAdd")
	purego.RegisterLibFunc(&k.interaction, sec, "SecKeychainSetUserInteractionAllowed")
	k.keys, e = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	if e != nil {
		k.err = e
		return k
	}
	k.values, e = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if e != nil {
		k.err = e
		return k
	}
	for _, name := range []string{"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecValueData", "kSecReturnData", "kSecMatchLimit", "kSecMatchLimitOne"} {
		sym, err := purego.Dlsym(sec, name)
		if err != nil {
			k.err = err
			return k
		}
		k.constant[name] = *(*uintptr)(unsafe.Pointer(sym))
	}
	sym, e := purego.Dlsym(cf, "kCFBooleanTrue")
	if e != nil {
		k.err = e
		return k
	}
	k.constant["true"] = *(*uintptr)(unsafe.Pointer(sym))
	return k
})

// DisableKeychainInteraction is called only by the dedicated background host.
// Automatic restore must report a need for authorization, never wait forever
// on a password prompt or repeatedly open one after a launchd restart.
func DisableKeychainInteraction() error {
	k := identitySecurity()
	if k.err != nil {
		return k.err
	}
	if k.interaction(false) != 0 {
		return ErrUserAction
	}
	return nil
}

func (k *identityKeychain) query(directory string) uintptr {
	q := k.dictCreate(0, 0, k.keys, k.values)
	k.dictSet(q, k.constant["kSecClass"], k.constant["kSecClassGenericPassword"])
	for name, value := range map[string]string{"kSecAttrService": "Shioji.Tailnet", "kSecAttrAccount": fmt.Sprintf("identity-%x", sha256.Sum256([]byte(directory)))} {
		s := k.stringCreate(0, value, 0x08000100)
		k.dictSet(q, k.constant[name], s)
		k.release(s)
	}
	return q
}

// IdentityKey preserves the historical service/account and encrypted file.
// Creation is used only by a user-initiated new enrollment in the network host.
func IdentityKey(directory string, create bool) ([]byte, error) {
	k := identitySecurity()
	if k.err != nil {
		return nil, ErrUserAction
	}
	q := k.query(directory)
	defer k.release(q)
	k.dictSet(q, k.constant["kSecReturnData"], k.constant["true"])
	k.dictSet(q, k.constant["kSecMatchLimit"], k.constant["kSecMatchLimitOne"])
	var data uintptr
	status := k.copy(q, &data)
	if status == 0 && data != 0 {
		defer k.release(data)
		n := k.dataLength(data)
		if n != 32 {
			return nil, ErrUserAction
		}
		return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(k.dataBytes(data))), n)...), nil
	}
	if status != -25300 || !create {
		return nil, ErrUserAction
	}
	if _, err := os.Lstat(filepath.Join(directory, "identity")); !os.IsNotExist(err) {
		return nil, ErrUserAction
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	add := k.query(directory)
	defer k.release(add)
	d := k.dataCreate(0, key, len(key))
	defer k.release(d)
	k.dictSet(add, k.constant["kSecValueData"], d)
	status = k.add(add, nil)
	if status == -25299 {
		clear(key)
		return IdentityKey(directory, false)
	}
	if status != 0 {
		clear(key)
		return nil, ErrUserAction
	}
	return key, nil
}

// CheckIdentity never creates, rewrites, promotes or replaces credentials.
func CheckIdentity(directory string, secret func() ([]byte, error)) error {
	_, err := os.Stat(filepath.Join(directory, "identity"))
	if os.IsNotExist(err) {
		intent, e := readIntent(directory)
		if e != nil {
			return e
		}
		if intent.Mode == "connected" || intent.Mode == "paused" {
			return ErrUserAction
		}
		return nil
	}
	if err != nil {
		return err
	}
	key, err := secret()
	if err != nil {
		return err
	}
	defer clear(key)
	if _, err = openStore(filepath.Join(directory, "identity"), key); err != nil {
		return ErrUserAction
	}
	return nil
}
