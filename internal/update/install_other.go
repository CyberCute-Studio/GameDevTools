//go:build !windows

package update

// heldByAnother reports no rename failure as one that may pass, as only
// Windows, the one platform the application runs on, keeps a file another
// program has open from being renamed.
func heldByAnother(error) bool {
	return false
}
