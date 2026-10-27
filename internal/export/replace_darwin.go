package export

import (
	"errors"

	"golang.org/x/sys/unix"
)

// exchange swaps the two directories atomically (renamex_np RENAME_SWAP), so
// readers of b see either the old tree or the new one. A file system without
// the swap, such as a non-APFS volume, falls back to two renames, which leave
// b missing for a moment.
func exchange(a, b string) error {
	e := unix.RenamexNp(a, b, unix.RENAME_SWAP)
	if !errors.Is(e, unix.ENOTSUP) && !errors.Is(e, unix.EOPNOTSUPP) && !errors.Is(e, unix.EINVAL) && !errors.Is(e, unix.ENOSYS) {
		return e
	}
	return swapByRenames(a, b)
}
