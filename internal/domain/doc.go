// Package domain holds entities, value objects, the status state machine and
// other pure rules. It must not import any project layer nor any I/O package
// (os, os/exec, net, io/fs, log, ...). Enforced by tools/archcheck.
// Contract: docs/02-DOMAIN-MODEL.md.
package domain
