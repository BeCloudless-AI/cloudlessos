//go:build linux

package privileged

import (
	"fmt"
	"net"
	"os/user"
	"strconv"
	"syscall"
)

func AuthorizePeer(controlGroup string) Authorizer {
	return func(conn net.Conn) error {
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			return fmt.Errorf("expected Unix connection")
		}
		raw, err := unixConn.SyscallConn()
		if err != nil {
			return err
		}
		var cred *syscall.Ucred
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			cred, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		}); err != nil {
			return err
		}
		if socketErr != nil {
			return socketErr
		}
		if cred.Uid == 0 {
			return nil
		}
		account, err := user.LookupId(strconv.FormatUint(uint64(cred.Uid), 10))
		if err != nil {
			return err
		}
		control, err := user.LookupGroup(controlGroup)
		if err != nil {
			return err
		}
		gids, err := account.GroupIds()
		if err != nil {
			gids = []string{account.Gid}
		}
		for _, gid := range gids {
			if gid == control.Gid {
				return nil
			}
		}
		return fmt.Errorf("uid %d is not a member of %s", cred.Uid, controlGroup)
	}
}

// AuthorizePeerUID admits exactly one unprivileged local account. It is used
// by the desktop-session agent, where admitting root would weaken the intended
// direction of trust between the system daemon and the logged-in desktop.
func AuthorizePeerUID(allowed uint32) Authorizer {
	return func(conn net.Conn) error {
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			return fmt.Errorf("expected Unix connection")
		}
		raw, err := unixConn.SyscallConn()
		if err != nil {
			return err
		}
		var cred *syscall.Ucred
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			cred, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		}); err != nil {
			return err
		}
		if socketErr != nil {
			return socketErr
		}
		if cred.Uid != allowed {
			return fmt.Errorf("uid %d is not the admitted desktop client", cred.Uid)
		}
		return nil
	}
}
