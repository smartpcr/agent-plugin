//go:build e2e && linux

package e2e

// The server side of the Stage 8.7 suite's NFS export (serveNFS in
// dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems_test.go).
// When this package's test binary starts with S87_NFS_BACKING set, it serves that
// directory as an NFSv3 export (mount and NFS programs on one TCP port, loopback
// only) as the calling user, prints "s87-nfs-ready <port>", and exits when its
// stdin closes or on SIGTERM or interrupt. It runs no test.

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

func init() {
	if backing := os.Getenv(s87NFSBackingEnv); backing != "" {
		os.Exit(s87ServeNFS(backing))
	}
}

// s87ChangeFS adds the attribute changes go-nfs needs to a billy filesystem.
type s87ChangeFS struct{ billy.Filesystem }

func (f s87ChangeFS) Chmod(name string, mode os.FileMode) error {
	return os.Chmod(f.Join(f.Root(), name), mode)
}

func (f s87ChangeFS) Lchown(name string, uid, gid int) error {
	return os.Lchown(f.Join(f.Root(), name), uid, gid)
}

func (f s87ChangeFS) Chown(name string, uid, gid int) error {
	return os.Chown(f.Join(f.Root(), name), uid, gid)
}

func (f s87ChangeFS) Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(f.Join(f.Root(), name), atime, mtime)
}

func s87ServeNFS(backing string) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	h := nfshelper.NewNullAuthHandler(s87ChangeFS{osfs.New(backing, osfs.WithBoundOS())})
	go func() { fmt.Fprintln(os.Stderr, nfs.Serve(l, nfshelper.NewCachingHandler(h, 4096))) }()
	fmt.Println(s87NFSReady, l.Addr().(*net.TCPAddr).Port)
	stop := make(chan struct{}, 2)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	go func() { <-sig; stop <- struct{}{} }()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop <- struct{}{} }()
	<-stop
	_ = l.Close()
	return 0
}
