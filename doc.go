/*
Package reversebin provides a Caddy HTTP handler (`reverse-bin`) that starts
an executable backend and proxies requests to it.

The module is intended for backends that should be started on demand and
terminated after inactivity.
*/
package reversebin
