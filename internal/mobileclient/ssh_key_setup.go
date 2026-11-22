package mobileclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var ErrSSHKeyRejected = errors.New("目标 Mac 尚未接受此设备的 SSH 公钥")

func isSSHAuthenticationRejection(err error) bool {
	// x/crypto/ssh returns an untyped error when the server exhausts the
	// available authentication methods. Host verification fails earlier.
	return err != nil && strings.HasPrefix(err.Error(), "ssh: handshake failed: ssh: unable to authenticate,")
}

// AuthorizeKeyWithPassword adds the device's public key to the SSH account
// on the reviewed Mac. The password is used only for this SSH connection.
func (e Endpoint) AuthorizeKeyWithPassword(ctx context.Context, signer ssh.Signer, route RouteDial, password string) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if password == "" {
		return errors.New("请输入目标 Mac 的 SSH 帐户密码")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, stop, err := e.authenticateWith(ctx, route, ssh.Password(password))
	if isSSHAuthenticationRejection(err) {
		return errors.New("目标 Mac 拒绝了密码登录，请核对帐户密码和 SSH 登录设置")
	}
	if err != nil {
		return err
	}
	defer stop()
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	session.Stdin = strings.NewReader(key + " Shioji-Mobile\n")
	session.Stdout, session.Stderr = io.Discard, io.Discard
	if err := session.Run("/bin/sh -c " + quote(authorizeSSHKeyScript)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("已登录，但无法写入 SSH 帐户的 authorized_keys：%w", err)
	}
	return nil
}

// Keep the lock on the same file while checking and appending. The leading
// newline also separates the new key from an existing unterminated record.
const authorizeSSHKeyScript = `set -eu
umask 077
IFS= read -r key
dir="$HOME/.ssh"
/bin/mkdir -p "$dir"
file="$dir/authorized_keys"
[ ! -e "$file" ] || [ -f "$file" ]
exec /usr/bin/lockf -k -t 5 "$file" /bin/sh -c '
  set -eu
  if /usr/bin/grep -Fqx -- "$1" "$2"; then
    exit 0
  else
    [ "$?" -eq 1 ]
  fi
  printf "\n%s\n" "$1" >> "$2"
' sh "$key" "$file"
`
