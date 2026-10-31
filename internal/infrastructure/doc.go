// Package infrastructure holds adapters that implement application ports:
// the agy process runner, JSON state store, markdown parser, git checkpointer,
// clock, flock, loggers. It may import domain and application, never
// presentation or cmd. Enforced by tools/archcheck.
package infrastructure
