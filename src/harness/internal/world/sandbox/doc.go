// Package sandbox is the contract of the sandbox seam (A10, areas/sandbox-ssh.md
// sec. 6.1) as far as the one process starter needs it before DH-S-13: the type
// Launch, which world/subprocess requires on every Spec, and Trusted, the plan for
// the host's own children.
//
// This is the smallest part of the contract (gap-closure plan step 19.3.6). The
// rest arrives at DH-S-13: Policy and policy.Resolver, sandbox.Service.Prepare,
// which builds a confined Launch from a Policy, the plan's Wrap, Confirm and
// Release that the starter calls around the start, the Confinement it reports,
// and the starter's refusal of Trusted when an agent initiated the start
// (DH-T-EXEC-8). Until then every start is a Trusted one and says why.
package sandbox
