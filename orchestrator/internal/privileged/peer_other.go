//go:build !linux

package privileged

import (
	"fmt"
	"net"
)

func AuthorizePeer(controlGroup string) Authorizer {
	return func(net.Conn) error {
		return fmt.Errorf("peer credential authorization is only supported on Linux")
	}
}

func AuthorizePeerUID(uint32) Authorizer {
	return func(net.Conn) error {
		return fmt.Errorf("peer credential authorization is only supported on Linux")
	}
}
