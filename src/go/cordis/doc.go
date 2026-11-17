// Package cordis is a Go port of cordiverse/cordis: a dynamic dependency-injection
// container in which service availability is a value that changes over time.
//
// Components declare the services they need. A component's fiber loads once every
// declared service is available, and unloads when one of them goes away or is
// replaced. Everything a component acquires while loaded is an effect: a resource
// paired with its cleanup, released in reverse order when the fiber unloads.
//
// The vocabulary follows upstream:
//
//   - A Context is the handle a component works through. Contexts form a tree; each
//     tree shares one service store, registry, event bus and logger.
//   - A Fiber is one live instantiation of a component. Its epoch (a reload revision
//     plus the identity of every injected provider) decides when it reloads.
//   - An effect is a resource tied to a fiber. Effects are released in reverse order,
//     so a tree always unloads from the leaves up.
//   - Isolate gives a subtree a private realm for a service name; Intercept layers
//     per-subtree configuration on a service.
//
// # Concurrency
//
// Every context of one tree shares a single lock. The runtime never runs component
// code while it holds that lock: notifications (fiber status, service changes) are
// queued and delivered in order once it is released, and component bodies, Init and
// effect cleanups run on the fiber's own goroutine. The exceptions are the
// Checker.Available method and the Schema of a component, which must be quick and
// must not call back into the context.
package cordis
