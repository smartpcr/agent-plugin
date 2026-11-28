//go:build e2e && linux

package e2e

// The server side of the Stage 8.7 suite's FUSE network filesystem (serveFUSE in
// dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems_test.go).
// When this package's test binary starts with S87_FUSE_BACKING and S87_FUSE_MOUNT
// set, it serves the backing directory at the mount point as an unprivileged FUSE
// loopback (mounted through the system's setuid fusermount3, as the calling user),
// prints s87-fuse-ready, and unmounts and exits when its stdin closes, on SIGTERM
// or interrupt, or when the mount is taken away. It runs no test.

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func init() {
	backing, mount := os.Getenv(s87FUSEBackingEnv), os.Getenv(s87FUSEMountEnv)
	if backing == "" || mount == "" {
		return
	}
	os.Exit(s87ServeFUSE(backing, mount))
}

func s87ServeFUSE(backing, mount string) int {
	root, err := fs.NewLoopbackRoot(backing)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The kernel keeps names and attributes for a second, as a network client
	// does, so a change behind the mount shows when the cache lapses.
	cache := time.Second
	srv, err := fs.Mount(mount, root, &fs.Options{
		EntryTimeout:    &cache,
		AttrTimeout:     &cache,
		NegativeTimeout: &cache,
		MountOptions:    fuse.MountOptions{FsName: "dsh-s87", Name: "dsh-s87"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(s87FUSEReady)
	stop := make(chan struct{}, 3)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	go func() { <-sig; stop <- struct{}{} }()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop <- struct{}{} }()
	go func() { srv.Wait(); stop <- struct{}{} }()
	<-stop
	if err := srv.Unmount(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return 0
}
