// Package audit holds the QLT-03 checks of what the application is built
// from and what it can reach: its dependencies and their licenses, the
// network code linked into it, the Windows libraries it loads, every place
// that changes a file, and the events its log can hold. It has no code of
// its own and nothing imports it; docs/privacy-verification.md records what
// the checks establish.
package audit
