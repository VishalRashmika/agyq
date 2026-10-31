// Package application holds use cases (orchestrator, recovery, limit waiter)
// and the PORT interfaces they depend on. It may import only internal/domain
// and context-style stdlib packages; time, filesystem, processes and signals
// reach it exclusively through ports. Enforced by tools/archcheck.
// Contract: docs/02-DOMAIN-MODEL.md section 2.
package application
