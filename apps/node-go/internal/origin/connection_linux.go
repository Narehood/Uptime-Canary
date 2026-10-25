package origin

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func protectConnectionFile(f *os.File) error { return f.Chmod(0600) }
func checkPrivateConnectionFile(f *os.File) error {
	var stat unix.Stat_t
	if unix.Fstat(int(f.Fd()), &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0600 || stat.Nlink != 1 {
		return errors.New("Node connection identity must be a private file owned by this account")
	}
	return nil
}
