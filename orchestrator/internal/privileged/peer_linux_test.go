//go:build linux

package privileged

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorizePeerUIDUsesKernelSocketCredentials(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "peer.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	type result struct {
		allowed error
		denied  error
	}
	results := make(chan result, 1)
	go func() {
		allowedConnection, acceptErr := listener.Accept()
		if acceptErr != nil {
			results <- result{allowed: acceptErr, denied: acceptErr}
			return
		}
		allowed := AuthorizePeerUID(uint32(os.Getuid()))(allowedConnection)
		_ = allowedConnection.Close()

		deniedConnection, acceptErr := listener.Accept()
		if acceptErr != nil {
			results <- result{allowed: allowed, denied: acceptErr}
			return
		}
		denied := AuthorizePeerUID(uint32(os.Getuid() + 1))(deniedConnection)
		_ = deniedConnection.Close()
		results <- result{allowed: allowed, denied: denied}
	}()

	for range 2 {
		connection, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}
	got := <-results
	if got.allowed != nil {
		t.Fatalf("configured UID was rejected: %v", got.allowed)
	}
	if got.denied == nil {
		t.Fatal("a different UID was admitted")
	}
}
