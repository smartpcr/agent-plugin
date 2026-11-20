// Package jsonrpc is the bounded, line-delimited JSON-RPC 2.0 codec shared by the
// harness and its clients (DH-S-4, DH-D-SDK-1). One frame is one line; a line has a
// byte bound, outbound calls a pending bound and inbound requests an in-flight
// bound. Every failure the codec reports is an *Error with a reason of the one
// "<domain>/<reason>" table, Table. It imports the standard library only (RM-3).
//
//dsh:untrusted-parser frames read from a peer process
package jsonrpc
