// Package allevents holds the persisted-type inventory of the whole program
// (DH-D-STORE-5, DH-T-STORE-10): its tests link every package that declares a
// durable event type on event.Default, so a shape change in any of them, not only
// in the core types package session/store sees, needs a version bump and a note.
// A source scan fails the tests when a package declares an event type the tests
// do not link. It has no code of its own.
package allevents
